package app_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestExporterSession_SubscribesBeforeHandoff proves the pass-2 RANK 1
// fix. The real kernel adapter's ExportOnConn returns immediately once
// the sysfs handoff lands; the matching detach uevent may then fire
// before the session handler gets a chance to Subscribe to KernelEvents.
// If the handler subscribes AFTER ExportOnConn, a fast kernel that
// publishes the detach event in the gap between ExportOnConn-returns
// and Subscribe loses the event, parking the handler forever.
//
// The preHandoffKernelEvents mock models that race deterministically:
// the set of subscribers is snapshotted AT THE MOMENT ExportOnConn is
// invoked. Subscribers registered after that moment receive a fresh
// channel (the API contract is preserved) but the pending detach
// event is broadcast ONLY to the pre-ExportOnConn set.
//
// Pre-fix: waitForSessionEnd calls Subscribe after kernel.ExportOnConn,
// so the event is lost and the handler parks. Test times out waiting
// for Sessions() to empty. Post-fix: the handler Subscribes BEFORE
// ExportOnConn, sees the pre-sent event, and unwinds.
func TestExporterSession_SubscribesBeforeHandoff(t *testing.T) {
	t.Parallel()

	const sessionBusID = domain.BusID("5-1")

	kev := newPreHandoffKernelEvents(sessionBusID)

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, id domain.BusID) error {
			require.Equal(t, sessionBusID, id)

			// Snapshot the subscriber set at the moment the kernel takes
			// the fd: anyone who was NOT already subscribed misses the
			// event published below.
			kev.closeSubscriptionWindow()

			// Publish the detach "uevent" immediately — the real kernel
			// can fire this the moment sysfs writes the usbip_sockfd
			// release. Handlers that subscribed after ExportOnConn
			// returned miss it; handlers that subscribed first receive
			// it on their buffered channel.
			kev.publishDetach()

			return nil
		},
	}

	codec := newSessionImportCodec(sessionBusID)

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 11), Port: 9600})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterEvents(kev),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	// Post-fix: the handler subscribed BEFORE ExportOnConn, received
	// the pre-ExportOnConn-published detach event on its buffered
	// channel, and unwound. Sessions() must empty within the settle
	// budget.
	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 0
	}, 2*time.Second, 10*time.Millisecond,
		"Sessions() must empty — handler subscribed before handoff so "+
			"the detach event published during ExportOnConn was delivered")

	cancel()

	<-serveDone
}

// preHandoffKernelEvents is a KernelEvents mock whose delivery policy
// distinguishes subscribers registered BEFORE ExportOnConn from those
// registered after. Subscribers in the pre-handoff set receive the
// next publishDetach event verbatim; subscribers arriving after
// closeSubscriptionWindow has fired get a channel that will never
// receive that event (the publish slot for post-window subscribers is
// dropped on the floor, modelling the lost-event race).
//
// Buffered channels (capacity 1) ensure publishDetach is non-blocking
// even if the consumer has not yet selected on the channel — the real
// kernel events buffer path is similarly non-blocking.
type preHandoffKernelEvents struct {
	mu         sync.Mutex
	busID      domain.BusID
	subs       []chan domain.Event
	windowOpen bool
}

// newPreHandoffKernelEvents returns a mock configured to deliver one
// detach event keyed to busID. The subscription window starts open and
// closes when closeSubscriptionWindow fires; publishDetach then
// broadcasts to every subscriber captured while the window was open.
func newPreHandoffKernelEvents(busID domain.BusID) *preHandoffKernelEvents {
	return &preHandoffKernelEvents{
		busID:      busID,
		windowOpen: true,
	}
}

// Subscribe implements app.KernelEvents. Subscribers that arrive while
// the window is open are added to the delivery set; subscribers after
// the window closes still receive a fresh channel (so the real API
// contract is preserved) but no event is ever pushed onto it.
func (k *preHandoffKernelEvents) Subscribe(_ context.Context) (<-chan domain.Event, func(), error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	ch := make(chan domain.Event, 1)

	if k.windowOpen {
		k.subs = append(k.subs, ch)
	}

	cancel := sync.OnceFunc(func() { close(ch) })

	return ch, cancel, nil
}

// closeSubscriptionWindow snapshots the subscriber set. Any Subscribe
// call after this runs is excluded from the pending publishDetach
// delivery. Called by the ExporterKernelMock.ExportOnConnFunc exactly
// once when the kernel takes the fd.
func (k *preHandoffKernelEvents) closeSubscriptionWindow() {
	k.mu.Lock()
	defer k.mu.Unlock()

	k.windowOpen = false
}

// publishDetach pushes a PortDetachedEvent for k.busID to every
// subscriber captured before the window closed. Uses a non-blocking
// send so the test never deadlocks on a slow consumer.
func (k *preHandoffKernelEvents) publishDetach() {
	k.mu.Lock()
	defer k.mu.Unlock()

	ev := domain.PortDetachedEvent{
		At:     time.Now(),
		Port:   domain.Port{BusID: k.busID},
		Reason: "kernel session-end",
	}

	for _, ch := range k.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// TestExporterShutdown_DisconnectsActiveSessions proves the pass-2
// RANK 3 fix. Closing handle.done alone only releases the handler from
// its event wait; the kernel still owns the socket until the app
// writes -1 to usbip_sockfd. Shutdown's graceful drain path MUST call
// e.kernel.Disconnect(ctx, busid) for every active session so the
// kernel releases the socket and the handler's waitForSessionEnd
// observes the kernel-emitted detach uevent.
//
// The test asserts Disconnect is invoked with the session's busid
// during Shutdown. Pre-fix Shutdown never calls Disconnect and the
// counter stays at 0; post-fix it is exactly 1.
func TestExporterShutdown_DisconnectsActiveSessions(t *testing.T) {
	t.Parallel()

	const sessionBusID = domain.BusID("5-5")

	// Buffered by 1 so the Disconnect hook's publish is non-blocking
	// even if the handler has not yet selected on the channel.
	events := make(chan domain.Event, 1)

	kev := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return events, func() {}, nil
		},
	}

	exportEntered := make(chan struct{}, 1)

	var disconnected atomic.Int32

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			select {
			case exportEntered <- struct{}{}:
			default:
			}

			return nil
		},
		DisconnectFunc: func(_ context.Context, id domain.BusID) error {
			require.Equal(t, sessionBusID, id)

			disconnected.Add(1)

			// Model the real kernel: Disconnect writes -1 to
			// usbip_sockfd; sysfs emits a remove uevent which the
			// KernelEvents netlink reader turns into a
			// PortDetachedEvent. The handler's waitForSessionEnd
			// matches on busid and unwinds.
			events <- domain.PortDetachedEvent{
				At:     time.Now(),
				Port:   domain.Port{BusID: sessionBusID},
				Reason: "kernel disconnect",
			}

			return nil
		},
	}

	codec := newSessionImportCodec(sessionBusID)

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 15), Port: 9100})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterEvents(kev),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	select {
	case <-exportEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("ExportOnConn was not invoked")
	}

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	require.NoError(t, exp.Shutdown(shutdownCtx),
		"Shutdown must drain gracefully once Disconnect is wired")

	require.Equal(t, int32(1), disconnected.Load(),
		"Shutdown must invoke kernel.Disconnect exactly once per active session")

	require.Empty(t, exp.Sessions(context.Background()),
		"Sessions() must be empty after Shutdown drains parked handlers")

	cancel()

	<-serveDone
}
