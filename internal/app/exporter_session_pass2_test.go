package app_test

import (
	"context"
	"errors"
	"io"
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

// TestExporterSession_ClosesAcceptedConnAfterSessionEnd proves the
// pass-2 RANK 4 fix. Per spec §5.4 the kernel dups the accepted fd on
// ExportOnConn success and holds its own ref; the app's original ref
// MUST be closed after the session ends so only the kernel's ref keeps
// the socket alive. Pre-fix, serveImport's handedOff=true suppresses
// the deferred close AND no later close fires on the success path,
// leaking the accepted conn for the full session lifetime plus
// whatever comes after.
//
// The test observes the close by wrapping the accepted conn through
// the project's countingListener helper: each accepted conn is a
// countingConn that bumps a counter on Close. A single session driven
// to a clean end MUST land exactly one close on the accepted conn.
func TestExporterSession_ClosesAcceptedConnAfterSessionEnd(t *testing.T) {
	t.Parallel()

	const sessionBusID = domain.BusID("5-2")

	events := make(chan domain.Event, 1)

	kev := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return events, func() {}, nil
		},
	}

	exportEntered := make(chan struct{}, 1)

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, id domain.BusID) error {
			require.Equal(t, sessionBusID, id)

			select {
			case exportEntered <- struct{}{}:
			default:
			}

			return nil
		},
	}

	codec := newSessionImportCodec(sessionBusID)

	lis := newCountingListener()

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

	// Trigger session end: the kernel-detach-uevent analogue.
	events <- domain.PortDetachedEvent{
		At:     time.Now(),
		Port:   domain.Port{BusID: sessionBusID},
		Reason: "kernel session-end",
	}

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 0
	}, 2*time.Second, 10*time.Millisecond,
		"Sessions() must empty after detach event")

	// Post-fix: the handler closes the accepted conn exactly once
	// after waitForSessionEnd returns. Pre-fix the handedOff guard
	// suppresses the deferred close on the success path and no other
	// close fires — closeCount stays 0.
	require.Eventually(t, func() bool {
		snap := lis.snapshot()

		return len(snap) == 1 && snap[0].closeCount() == 1
	}, 2*time.Second, 10*time.Millisecond,
		"accepted conn must be closed exactly once after session end")

	cancel()

	<-serveDone
}

// errSubscribeFailed is the mock sentinel returned by the subscribe-
// failure KernelEvents stub so the test can assert the handler error
// path without relying on slog output.
var errSubscribeFailed = errors.New("subscribe failed (mock)")

// TestExporterSession_ClosesAcceptedConnOnSubscribeFailure exercises
// the RANK 5 error branch folded into RANK 4: when Subscribe fails in
// the pre-handoff path the handler returns early. The accepted conn
// must still be closed on handler exit regardless of handedOff.
func TestExporterSession_ClosesAcceptedConnOnSubscribeFailure(t *testing.T) {
	t.Parallel()

	const sessionBusID = domain.BusID("5-3")

	kev := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return nil, nil, errSubscribeFailed
		},
	}

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			return nil
		},
	}

	codec := newSessionImportCodec(sessionBusID)

	lis := newCountingListener()

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

	require.Eventually(t, func() bool {
		snap := lis.snapshot()

		return len(snap) == 1 && snap[0].closeCount() == 1
	}, 2*time.Second, 10*time.Millisecond,
		"accepted conn must close on subscribe-failure path")

	cancel()

	<-serveDone
}

// TestExporterSession_ClosesAcceptedConnOnEventsChannelClosed covers
// the second RANK 5 error branch: the KernelEvents source channel
// closes before any matching event arrives. The handler interprets
// that as a kernel-side teardown and exits; the accepted conn must
// still close exactly once.
func TestExporterSession_ClosesAcceptedConnOnEventsChannelClosed(t *testing.T) {
	t.Parallel()

	const sessionBusID = domain.BusID("5-4")

	events := make(chan domain.Event)

	kev := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return events, func() {}, nil
		},
	}

	exportEntered := make(chan struct{}, 1)

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			select {
			case exportEntered <- struct{}{}:
			default:
			}

			return nil
		},
	}

	codec := newSessionImportCodec(sessionBusID)

	lis := newCountingListener()

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

	// Source teardown before any event arrives: handler exits via
	// the "chan closed" branch.
	close(events)

	require.Eventually(t, func() bool {
		snap := lis.snapshot()

		return len(snap) == 1 && snap[0].closeCount() == 1
	}, 2*time.Second, 10*time.Millisecond,
		"accepted conn must close on events-channel-closed path")

	cancel()

	<-serveDone
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

// TestExporterShutdown_TimeoutIsMinOfCtxAndConfig proves the pass-2
// RANK 7 fix. applyShutdownBackstop must derive the drain deadline as
// min(ctx deadline, configured shutdownTimeout). Pre-fix, ANY
// caller-supplied deadline disables the backstop entirely, so a
// caller with a generous 10s ctx but a 50ms configured timeout waits
// 10s despite the explicit configuration.
//
// The test pins the kernel in a forever-wedged ExportOnConn so
// Shutdown has to rely on the backstop to unwedge. It passes a ctx
// with a generous 10s deadline and a configured 50ms shutdown timeout;
// post-fix Shutdown returns in ~50ms; pre-fix it waits up to 10s.
func TestExporterShutdown_TimeoutIsMinOfCtxAndConfig(t *testing.T) {
	t.Parallel()

	exportStarted := make(chan struct{}, 1)

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, c net.Conn, _ domain.BusID) error {
			select {
			case exportStarted <- struct{}{}:
			default:
			}

			// Block on the conn; only the backstop's force-close can
			// unwedge this drain. io.Copy returns io.EOF once the
			// backstop closes the conn — the error surface stays
			// stdlib-sourced so wrapcheck is happy.
			_, _ = io.Copy(io.Discard, c)

			return io.EOF
		},
		// Pass-2 RANK 3: Shutdown now issues Disconnect per session;
		// the scenario deliberately models a kernel that silently
		// swallows Disconnect without emitting the matching uevent,
		// forcing the backstop deadline to be the only exit path.
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	codec := newSessionImportCodec(domain.BusID("5-6"))

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 16), Port: 9200})

	const configTimeout = 50 * time.Millisecond

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterShutdownTimeout(configTimeout),
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
	case <-exportStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("ExportOnConn never started")
	}

	// Caller supplies a GENEROUS deadline; the config's tighter
	// timeout MUST win.
	const callerBudget = 10 * time.Second

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), callerBudget)
	defer shutdownCancel()

	start := time.Now()

	_ = exp.Shutdown(shutdownCtx)

	elapsed := time.Since(start)

	require.Less(t, elapsed, callerBudget/2,
		"Shutdown timeout must be min(ctx deadline, configured timeout); "+
			"pre-fix any ctx deadline disables the backstop and Shutdown "+
			"waits the caller budget instead of the configured %s", configTimeout)

	cancel()

	<-serveDone
}
