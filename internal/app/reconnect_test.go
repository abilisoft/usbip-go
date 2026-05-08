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

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// reconnectTestBackoff returns a deterministic FixedBackoff the watcher
// tests can drive through FakeClock without any jitter noise.
func reconnectTestBackoff() app.FixedBackoff {
	return app.FixedBackoff{Delay: 250 * time.Millisecond}
}

// reconnectTestSettleBudget bounds how long a test is willing to wait for
// a goroutine state transition (channel send, subscribe installation).
// Used only as a safety net — the test logic itself is signal-driven.
const reconnectTestSettleBudget = 2 * time.Second

// eventChannelRegistry captures every channel returned by the mocked
// KernelEvents.Subscribe so each test can target a specific watcher's
// channel deterministically. Per-watcher channels sidestep the fan-out
// race that would otherwise surface if all watchers shared one channel.
type eventChannelRegistry struct {
	mu   sync.Mutex
	chs  []chan domain.Event
	wait chan struct{}
}

func newEventChannelRegistry() *eventChannelRegistry {
	return &eventChannelRegistry{wait: make(chan struct{}, 8)}
}

// subscribe returns a fresh buffered channel and stores it in the
// registry so the test can reach it back.
func (r *eventChannelRegistry) subscribe() (<-chan domain.Event, func()) {
	ch := make(chan domain.Event, 4)

	r.mu.Lock()
	r.chs = append(r.chs, ch)
	r.mu.Unlock()

	select {
	case r.wait <- struct{}{}:
	default:
	}

	cancel := sync.OnceFunc(func() { close(ch) })

	return ch, cancel
}

// waitFor blocks until at least n channels have been registered or the
// deadline fires. Tests rely on this to synchronise with the watcher's
// subscribe call before they send an event.
func (r *eventChannelRegistry) waitFor(t *testing.T, n int) {
	t.Helper()

	deadline := time.After(reconnectTestSettleBudget)

	for {
		r.mu.Lock()

		got := len(r.chs)

		r.mu.Unlock()

		if got >= n {
			return
		}

		select {
		case <-r.wait:
		case <-deadline:
			t.Fatalf("only %d/%d subscribe channels registered within %s", got, n, reconnectTestSettleBudget)
		}
	}
}

// channel returns the idx-th registered channel.
func (r *eventChannelRegistry) channel(t *testing.T, idx int) chan domain.Event {
	t.Helper()

	r.mu.Lock()
	defer r.mu.Unlock()

	require.Less(t, idx, len(r.chs), "no subscribe channel at index %d", idx)

	return r.chs[idx]
}

// newReconnectFixture wires an Importer that drives the reconnect watcher
// against deterministic mocks. attachRemoteFn lets each test control the
// per-call behaviour of kernel.AttachRemote (typically returning a fresh
// PortID per call and optionally failing).
//
// The fixture returns the Importer, the fake clock, the events registry,
// and the ImporterKernel mock so tests can add expectations on it.
func newReconnectFixture(
	t *testing.T,
	attachRemoteFn func(ctx context.Context, conn net.Conn, spec app.RemoteDeviceSpec) (domain.PortID, error),
) (*app.Importer, *testutil.FakeClock, *eventChannelRegistry, *ImporterKernelMock) {
	t.Helper()

	registry := newEventChannelRegistry()

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			ch, cancel := registry.subscribe()

			return ch, cancel, nil
		},
	}

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc:     attachRemoteFn,
		DetachPortFunc:       func(_ context.Context, _ domain.PortID) error { return nil },
		ListPortsFunc:        func(_ context.Context) ([]domain.Port, error) { return nil, nil },
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	clk := testutil.NewFakeClockAt(importerTestEpoch())

	imp := app.NewImporter(
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(events),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterClock(clk),
	)

	return imp, clk, registry, kernel
}

// attachOptionsWithBackoff returns an AttachOptions configured with the
// canonical test backoff and AutoReconnect enabled. Tests override fields
// (MaxAttempts, OnReconnect, StatusPollInterval) by mutating the returned
// value before passing it to Attach.
func attachOptionsWithBackoff() app.AttachOptions {
	return app.AttachOptions{
		AutoReconnect:      true,
		Backoff:            reconnectTestBackoff(),
		StatusPollInterval: -1, // disable poll backstop; uevent tests drive detection
	}
}

// TestImporterReconnectUeventTriggersReattach proves the primary
// detection path per spec §5.5 item 1: a remote-device uevent that maps
// to a PortDetachedEvent for our port triggers one reconnect attempt
// after the backoff delay, yielding two AttachRemote invocations (the
// initial attach + the reconnect).
func TestImporterReconnectUeventTriggersReattach(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	// Fire the uevent for our port id — watcher must move to the
	// reconnect phase, invoke the clock for backoff, and reattach.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// The clock must keep ticking so the After timer eventually
	// fires: the watcher may not have registered its pending When
	// yet, so one pre-register Advance is not enough. Eventually
	// polls with Advance inside so any interleaving succeeds.
	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond, "AttachRemote should be called exactly twice after reconnect")

	// Replacement watcher subscribes from the successful Attach path.
	registry.waitFor(t, 2)
}

// TestImporterReconnectPollTriggersReattach covers the backstop path
// per spec §5.5 item 2: the uevent source stays silent, but a periodic
// ListPorts poll observes our PortID in StatusNull → watcher reattaches.
func TestImporterReconnectPollTriggersReattach(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	// Poll returns the initial port as Used; after the first call,
	// swap the reply so the watcher observes StatusNull and reconnects.
	var pollCount atomic.Int32

	kernel.ListPortsFunc = func(_ context.Context) ([]domain.Port, error) {
		call := pollCount.Add(1)
		if call == 1 {
			return []domain.Port{{ID: 1, Status: domain.StatusUsed}}, nil
		}

		return []domain.Port{{ID: 1, Status: domain.StatusNull}}, nil
	}

	opts := attachOptionsWithBackoff()

	opts.StatusPollInterval = 5 * time.Second

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	// Advance the clock at each poll-interval step until two poll
	// calls have happened (the first returns StatusUsed, the second
	// StatusNull) plus one backoff step for the reconnect attempt.
	// Eventually re-advances each tick so we are robust to the exact
	// moment the watcher registers its pending After channels.
	require.Eventually(t, func() bool {
		clk.Advance(opts.StatusPollInterval)
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 10*time.Millisecond, "poll-detected detach must produce exactly one reconnect")

	registry.waitFor(t, 2)
}

// TestImporterReconnectBackoffRespected asserts the watcher sleeps for
// exactly Backoff.Next(attempt) between attempts. OnReconnect firing is
// the deterministic sync point that the watcher has entered the
// backoff sleep; from there we can assert the clock must advance by
// the full delay before the reconnect runs.
func TestImporterReconnectBackoffRespected(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	onReconnectFired := make(chan struct{}, 1)

	opts := attachOptionsWithBackoff()

	opts.OnReconnect = func(_ int, _ error) {
		select {
		case onReconnectFired <- struct{}{}:
		default:
		}
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// Synchronise on OnReconnect so we know the watcher is now inside
	// waitBackoff; past this point the backoff deadline is registered
	// with the FakeClock and Advance is deterministic.
	select {
	case <-onReconnectFired:
	case <-time.After(reconnectTestSettleBudget):
		t.Fatal("OnReconnect did not fire")
	}

	// Advance by LESS than the backoff — reconnect must NOT fire yet.
	clk.Advance(reconnectTestBackoff().Delay / 2)

	require.Never(t, func() bool {
		return len(kernel.AttachRemoteCalls()) > 1
	}, 50*time.Millisecond, 5*time.Millisecond, "AttachRemote must not fire before backoff elapses")

	// Advance the remainder — now the reconnect runs.
	clk.Advance(reconnectTestBackoff().Delay / 2)

	require.Eventually(t, func() bool {
		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond)
}

// TestImporterReconnectMaxAttemptsExhausts asserts MaxAttempts=3 caps
// the retries: initial attach + 3 reconnect attempts = 4 AttachRemote
// calls, after which the watcher exits without further attempts.
func TestImporterReconnectMaxAttemptsExhausts(t *testing.T) {
	t.Parallel()

	var attachCount atomic.Int32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		n := attachCount.Add(1)
		if n == 1 {
			return domain.PortID(1), nil
		}

		return 0, errBoom
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)

	var reconnects atomic.Int32

	opts := attachOptionsWithBackoff()

	opts.MaxAttempts = 3
	opts.OnReconnect = func(_ int, _ error) { reconnects.Add(1) }

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// Drive the watcher through three full attempts. Each iteration
	// advances the clock until AttachRemote has been invoked (i.e. the
	// backoff elapsed for this attempt).
	for i := range 3 {
		want := int32(i + 2) // +1 for the initial attach, +1 for this reconnect
		require.Eventually(t, func() bool {
			clk.Advance(reconnectTestBackoff().Delay)

			return attachCount.Load() >= want
		}, reconnectTestSettleBudget, 10*time.Millisecond,
			"AttachRemote should reach %d calls after attempt %d", want, i+1)
	}

	// Close waits on the Importer waitgroup — if the watcher were still
	// looping, Close would never return. That drain is the assertion:
	// the watcher exited after MaxAttempts.
	require.NoError(t, imp.Close())

	require.Equal(t, int32(3), reconnects.Load(), "OnReconnect must fire exactly MaxAttempts times")
	require.Len(t, kernel.AttachRemoteCalls(), 4, "initial attach + 3 reconnect attempts")
}

// TestImporterReconnectDetachCancelsWatcher covers the §5.5 cancellation
// contract: a Detach that lands while the watcher is asleep between
// attempts tears the watcher down without a further reconnect attempt.
func TestImporterReconnectDetachCancelsWatcher(t *testing.T) {
	t.Parallel()

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(1), nil
	}

	imp, _, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// Do NOT advance the clock — the watcher is now parked in the
	// backoff sleep. Detach must cancel it before a reconnect can run.
	require.NoError(t, imp.Detach(context.Background(), port.ID))

	require.Len(t, kernel.AttachRemoteCalls(), 1, "cancellation must suppress the reconnect attempt")
}

// TestImporterReconnectCloseCancelsWatcher mirrors the Detach test for
// the Close codepath: Close must cancel every outstanding watcher and
// drain via the Importer's waitgroup.
func TestImporterReconnectCloseCancelsWatcher(t *testing.T) {
	t.Parallel()

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(1), nil
	}

	imp, _, registry, kernel := newReconnectFixture(t, attachFn)

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// Close must return promptly even though the watcher is parked in
	// the backoff sleep — it cancels every handle then drains the wg.
	require.NoError(t, imp.Close())

	require.Len(t, kernel.AttachRemoteCalls(), 1, "Close must cancel the pending reconnect")
}

// TestImporterReconnectStaleEventIgnored asserts the generation filter:
// after a reconnect succeeds and the PortID changes, a late uevent for
// the OLD port id must not trigger a second reconnect.
func TestImporterReconnectStaleEventIgnored(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond)

	registry.waitFor(t, 2)

	// New watcher is now subscribed on registry channel index 1 and
	// tracks port id 2. A stale event for port id 1 MUST NOT cause a
	// third AttachRemote call.
	registry.channel(t, 1) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Never(t, func() bool {
		return len(kernel.AttachRemoteCalls()) > 2
	}, 50*time.Millisecond, 5*time.Millisecond, "stale old-port-id event must be ignored by the new watcher")
}

// TestImporterReconnectDefaultsApplied covers the zero-value branches
// in resolveReconnectOptions: omitted Backoff and StatusPollInterval
// get their §5.5 defaults. We exercise the path by attaching with
// AutoReconnect=true but no backoff / poll overrides, then detach to
// ensure the watcher shut down cleanly (which it cannot do if defaults
// panicked the zero-value struct).
func TestImporterReconnectDefaultsApplied(t *testing.T) {
	t.Parallel()

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(1), nil
	}

	imp, _, registry, _ := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(
		context.Background(),
		testRemote(),
		attachBusID(),
		app.AttachOptions{AutoReconnect: true},
	)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	// Detach cancels the watcher; if defaults had panicked the watcher
	// goroutine, the watcherDone close would never happen and Detach
	// would block forever — a timeout here would fail the test.
	require.NoError(t, imp.Detach(context.Background(), port.ID))
}

// TestImporterReconnectSubscribeFailureExits asserts the watcher exits
// cleanly when KernelEvents.Subscribe rejects the request. Close must
// still drain the waitgroup.
func TestImporterReconnectSubscribeFailureExits(t *testing.T) {
	t.Parallel()

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return nil, nil, errBoom
		},
	}

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return domain.PortID(1), nil
		},
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := app.NewImporter(
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(events),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterClock(testutil.NewFakeClockAt(importerTestEpoch())),
	)

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)

	// Close drains i.wg — if the watcher leaked, Close would never
	// return. The subscribe-failure branch must exit the goroutine.
	require.NoError(t, imp.Close())
}

// TestImporterReconnectEventsChannelCloseExits mirrors the subscribe
// failure path for the runtime case: the upstream source closes the
// events channel while the watcher is waiting. The watcher must treat
// that as cancellation and exit.
func TestImporterReconnectEventsChannelCloseExits(t *testing.T) {
	t.Parallel()

	// A pre-closed subscribe channel triggers the watcher's
	// closed-channel select branch immediately after subscription; the
	// goroutine must exit so Close can drain the waitgroup.
	preClosed := make(chan domain.Event)

	close(preClosed)

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return preClosed, func() {}, nil
		},
	}

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return domain.PortID(1), nil
		},
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := app.NewImporter(
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(events),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterClock(testutil.NewFakeClockAt(importerTestEpoch())),
	)

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)

	require.NoError(t, imp.Close())
}

// TestImporterReconnectIgnoresForeignEvents asserts the watcher
// discards PortDetachedEvents that carry a different port id and every
// non-detach event kind; only a matching detach triggers the reconnect.
func TestImporterReconnectIgnoresForeignEvents(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	// Foreign events: a detach for a different port id and an
	// attached-event for our port id — neither is a reconnect signal.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: domain.PortID(99)}}

	registry.channel(t, 0) <- domain.PortAttachedEvent{Port: domain.Port{ID: port.ID}}

	// Matching detach finally arrives — reconnect must fire for THIS.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond)
}

// TestImporterReconnectPollListPortsErrorTolerated asserts the watcher
// survives a ListPorts error: the poll tick returns false (debug
// logged) and the next poll cycle still runs, so a subsequent
// StatusNull observation still triggers the reconnect.
func TestImporterReconnectPollListPortsErrorTolerated(t *testing.T) {
	t.Parallel()

	var (
		nextID      atomic.Uint32
		listCalls   atomic.Int32
		advanceStep = 250 * time.Millisecond
	)

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	kernel.ListPortsFunc = func(_ context.Context) ([]domain.Port, error) {
		n := listCalls.Add(1)
		if n == 1 {
			return nil, errBoom
		}

		return []domain.Port{{ID: 1, Status: domain.StatusNull}}, nil
	}

	opts := attachOptionsWithBackoff()

	opts.StatusPollInterval = advanceStep

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	require.Eventually(t, func() bool {
		clk.Advance(advanceStep)
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 10*time.Millisecond,
		"reconnect must still fire after transient ListPorts error")
}

// TestImporterReconnectAttachClosedHaltsLoop asserts that when the
// Importer closes mid-reconnect (AttachRemote succeeds but Close
// flipped closed=true before registerHandle could commit), the
// watcher's recursive Attach returns ErrImporterClosed and the loop
// short-circuits — the errors.Is(err, ErrImporterClosed) branch exits
// without further attempts.
func TestImporterReconnectAttachClosedHaltsLoop(t *testing.T) {
	t.Parallel()

	var (
		attachCount atomic.Int32
		entered     = make(chan struct{}, 1)
		release     = make(chan struct{})
	)

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		n := attachCount.Add(1)
		if n == 1 {
			return domain.PortID(1), nil
		}

		// The second call (reconnect attempt) parks until the test
		// closes the Importer; Close flips closed=true, then releases
		// the gate so AttachRemote returns. registerHandle then sees
		// closed=true and bounces ErrImporterClosed back to the
		// watcher, which exits its loop.
		entered <- struct{}{}

		<-release

		return domain.PortID(2), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// Advance until the watcher has entered the second AttachRemote.
	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		select {
		case <-entered:
			return true
		default:
			return false
		}
	}, reconnectTestSettleBudget, 10*time.Millisecond)

	closeDone := make(chan error, 1)

	go func() { closeDone <- imp.Close() }()

	// Spin until Close has flipped closed=true (observable via
	// ListPorts returning ErrImporterClosed). Only then release the
	// gate so AttachRemote's caller sees the closed state inside
	// registerHandle.
	require.Eventually(t, func() bool {
		_, lpErr := imp.ListPorts(context.Background())

		return errors.Is(lpErr, app.ErrImporterClosed)
	}, reconnectTestSettleBudget, 5*time.Millisecond)

	close(release)

	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(reconnectTestSettleBudget):
		t.Fatal("Close did not drain after the watcher short-circuited on ErrImporterClosed")
	}

	require.Len(t, kernel.AttachRemoteCalls(), 2, "watcher must stop after ErrImporterClosed")
}

// TestImporterReconnectZeroBackoffSkipsSleep covers the early-return
// branch in waitBackoff when Backoff.Next(0) yields a non-positive
// duration. With FixedBackoff{Delay: 0}, the watcher must reconnect
// without advancing the clock.
func TestImporterReconnectZeroBackoffSkipsSleep(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, _, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	opts := app.AttachOptions{
		AutoReconnect:      true,
		Backoff:            app.FixedBackoff{Delay: 0},
		StatusPollInterval: -1,
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"zero-delay backoff must reconnect without clock advance")
}

// TestImporterReconnectSameSlotStaleEventIgnored proves the generation
// filter promised by spec §5.5: when the kernel re-uses the SAME PortID
// slot after a detach (a legitimate kernel behaviour: vhci_hcd picks the
// lowest free slot), a late uevent for that id that targets the OLD
// generation must NOT fire a third reconnect. Id-equality alone is not
// enough — the watcher must confirm with the kernel that the port is
// actually detached before reacting. Matching purely on Port.ID would
// let a stale event for port 1 at the new watcher (whose port is still
// alive) trigger a bogus reconnect.
func TestImporterReconnectSameSlotStaleEventIgnored(t *testing.T) {
	t.Parallel()

	// AttachRemote returns portID=1 on BOTH calls — same-slot reuse.
	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(1), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	// After the first reconnect succeeds, any subsequent ListPorts call
	// (the kernel-confirmation backstop) reports portID=1 as StatusUsed —
	// i.e., the kernel view is healthy and the stale event is obsolete.
	var attachedOnce atomic.Bool

	kernel.ListPortsFunc = func(_ context.Context) ([]domain.Port, error) {
		if attachedOnce.Load() {
			return []domain.Port{{ID: 1, Status: domain.StatusUsed}}, nil
		}

		return nil, nil
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	// Fire the first detach on the original watcher's channel. It must
	// reconnect successfully — AttachRemote is now 2.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond, "first reconnect must succeed")

	// Replacement watcher subscribes.
	registry.waitFor(t, 2)

	// From here on, the kernel view says portID=1 is healthy.
	attachedOnce.Store(true)

	// Push a stale detach event (generated by the kernel BEFORE the
	// successful reattach) onto the new watcher's channel. The new
	// watcher's port is legitimately alive — ListPorts confirms it —
	// so the watcher must reject the event, not fire a third reconnect.
	registry.channel(t, 1) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Never(t, func() bool {
		// Drive backoff in case the watcher erroneously parked in it.
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) > 2
	}, 100*time.Millisecond, 5*time.Millisecond,
		"same-slot stale event must be filtered by kernel-confirmation, not by port id")
}

// TestImporterReconnectOnReconnectPanicRecovered asserts the watcher
// survives an OnReconnect callback that panics. Per spec §5.5 the
// callback is a fire-and-forget notification, so a buggy caller must
// not crash the process, wedge the watcher's retry cadence, or leak a
// goroutine (goleak's TestMain hook backs up the assertion).
func TestImporterReconnectOnReconnectPanicRecovered(t *testing.T) {
	t.Parallel()

	var attachCount atomic.Int32

	// First AttachRemote succeeds (initial Attach). Every subsequent
	// AttachRemote (the reconnect attempts) fails so the watcher stays
	// in its retry loop long enough for the panic to surface AND for
	// the next attempt to happen.
	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		n := attachCount.Add(1)
		if n == 1 {
			return domain.PortID(1), nil
		}

		return 0, errBoom
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)

	var (
		callbackCount atomic.Int32
		panicFired    = make(chan struct{}, 1)
	)

	opts := attachOptionsWithBackoff()

	opts.MaxAttempts = 3
	opts.OnReconnect = func(attempt int, _ error) {
		callbackCount.Add(1)

		if attempt == 1 {
			select {
			case panicFired <- struct{}{}:
			default:
			}

			panic("test panic from OnReconnect")
		}
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// Drive the watcher through all three attempts. Each iteration
	// advances the clock until AttachRemote has been invoked for the
	// expected attempt. If the panic wedged the watcher, the second
	// attempt never lands and Eventually times out.
	for i := range 3 {
		want := int32(i + 2) // +1 initial attach, +1 per attempt
		require.Eventually(t, func() bool {
			clk.Advance(reconnectTestBackoff().Delay)

			return attachCount.Load() >= want
		}, reconnectTestSettleBudget, 10*time.Millisecond,
			"watcher must continue after OnReconnect panic (expected attempt %d)", i+1)
	}

	// The panic from attempt 1 actually reached the test's callback.
	select {
	case <-panicFired:
	case <-time.After(reconnectTestSettleBudget):
		t.Fatal("OnReconnect was never invoked; fixture is broken")
	}

	// Close drains the waitgroup — a leaked watcher goroutine would
	// deadlock here. This is the direct assertion that the panic did
	// not leave the watcher alive and orphaned.
	require.NoError(t, imp.Close())

	require.Equal(t, int32(3), callbackCount.Load(),
		"callback must still fire for every attempt despite the first-attempt panic")
	require.Len(t, kernel.AttachRemoteCalls(), 4, "initial + 3 reconnect attempts")
}

// TestImporterReconnectSupersededWatcherDropsEvent exercises the
// "!isCurrentHandle" guard inside isDetachSignal: when a second Attach
// has replaced the handle map entry for this watcher's portID but the
// watcher's select reads the event branch before its ctx cancellation
// propagates, the watcher must drop the event rather than fire a
// spurious reconnect. The test drives the race by pushing a detach
// event for port 1 into watcher1's channel and then performing a
// second Attach for the same slot; the event is buffered before the
// second Attach lands, so watcher1's select sees both branches ready.
func TestImporterReconnectSupersededWatcherDropsEvent(t *testing.T) {
	t.Parallel()

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(1), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	// Poll path is disabled so only the uevent branch can fire a
	// reconnect — isolates the coverage to isDetachSignal.
	opts := attachOptionsWithBackoff()

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	// Buffer a detach event on watcher1's channel BEFORE the second
	// Attach. The second Attach will replace map[1] with handle2 and
	// cancel handle1. Watcher1's select now races: it may observe the
	// buffered event (which must be dropped by isCurrentHandle) or its
	// ctx-cancelled signal (which exits cleanly). Either branch must
	// keep AttachRemote count at 2.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	_, err = imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 2)

	// Watcher2 is healthy; advancing the clock does nothing because the
	// poll is disabled and no event has been pushed to watcher2. The
	// only way AttachRemote climbs to 3 is if watcher1 processes the
	// stale event — the generation check must prevent that.
	require.Never(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) > 2
	}, 100*time.Millisecond, 5*time.Millisecond,
		"superseded watcher must not reconnect after its handle is replaced")
}

// TestImporterReconnectDetachShutdownTimeoutDisabled asserts that a
// negative ShutdownTimeout opts the Detach wait out of the bound —
// the wait reverts to the pre-Fix-3 semantics (block on watcherDone
// indefinitely). The watcher here exits promptly so the unbounded wait
// completes immediately; the assertion is simply that Detach returns
// cleanly, proving the branch is live.
func TestImporterReconnectDetachShutdownTimeoutDisabled(t *testing.T) {
	t.Parallel()

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(1), nil
	}

	imp, _, registry, _ := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	opts := attachOptionsWithBackoff()

	opts.ShutdownTimeout = -1 // disables the bound — infinite wait.

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	require.NoError(t, imp.Detach(context.Background(), port.ID),
		"negative ShutdownTimeout must fall through to the legacy infinite wait")
}

// TestImporterReconnectCloseShutdownTimeoutDisabled asserts the same
// negative-ShutdownTimeout branch on Close's wg.Wait bound: when the
// longest handle opts out of the bound, Close falls back to unbounded
// i.wg.Wait. The watcher drains fast in this test so the behaviour is
// indistinguishable from the bounded path in wall-clock terms, but the
// negative-timeout code path is exercised.
func TestImporterReconnectCloseShutdownTimeoutDisabled(t *testing.T) {
	t.Parallel()

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(1), nil
	}

	imp, _, registry, _ := newReconnectFixture(t, attachFn)

	opts := attachOptionsWithBackoff()

	opts.ShutdownTimeout = -1

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	require.NoError(t, imp.Close(), "Close must drain watchers under the unbounded branch")
}

// TestImporterReconnectDetachShutdownTimeoutBounded asserts Detach
// returns within AttachOptions.ShutdownTimeout even when the watcher is
// wedged (here: the reconnect-path AttachRemote ignores ctx cancellation
// and blocks forever, so h.watcherDone never closes on its own). Per
// spec §5.5, Detach's wait on watcherDone is bounded; pre-fix the wait
// was unbounded and Detach would hang indefinitely.
func TestImporterReconnectDetachShutdownTimeoutBounded(t *testing.T) {
	t.Parallel()

	var (
		attachCount atomic.Int32
		release     = make(chan struct{})
	)

	// First AttachRemote (initial Attach) succeeds. The second call is
	// the reconnect attempt — it blocks until the test releases it,
	// IGNORING ctx cancellation. This wedges the watcher: h.cancel()
	// fires from Detach, but the watcher is parked inside AttachRemote
	// and cannot observe ctx.Done().
	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		n := attachCount.Add(1)
		if n == 1 {
			return domain.PortID(1), nil
		}

		<-release

		return 0, errBoom
	}

	imp, clk, registry, _ := newReconnectFixture(t, attachFn)
	t.Cleanup(func() {
		// Release the stuck attach so Close drains cleanly AFTER the
		// test has verified the bounded wait.
		close(release)
		require.NoError(t, imp.Close())
	})

	opts := attachOptionsWithBackoff()

	opts.ShutdownTimeout = 50 * time.Millisecond

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	// Trigger the reconnect path — watcher moves into the blocking
	// second AttachRemote.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// Spin the clock until the watcher is parked inside AttachRemote.
	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return attachCount.Load() >= 2
	}, reconnectTestSettleBudget, 5*time.Millisecond, "reconnect attempt must enter AttachRemote")

	// Detach with a wedged watcher: must return within ShutdownTimeout
	// (plus scheduling slack) — pre-fix it would hang forever waiting
	// on watcherDone.
	detachDone := make(chan error, 1)

	go func() {
		detachDone <- imp.Detach(context.Background(), port.ID)
	}()

	// Advance the fake clock past the ShutdownTimeout so the bounded
	// wait's timer fires.
	require.Eventually(t, func() bool {
		clk.Advance(opts.ShutdownTimeout)

		select {
		case <-detachDone:
			return true
		default:
			return false
		}
	}, 200*time.Millisecond, 5*time.Millisecond,
		"Detach must return within ShutdownTimeout despite wedged watcher")
}

// TestReconnect_RegistersBackoffDeadlineBeforeOnReconnect pins the
// ordering invariant that eliminates the fireOnReconnect / waitBackoff
// race: the reconnect loop MUST register the backoff deadline on the
// watcher goroutine BEFORE firing OnReconnect. Tests that synchronise
// on OnReconnect firing (TestImporterReconnectBackoffRespected being
// the canonical one) then call clk.Advance(delay) and expect the
// backoff channel to fire; pre-fix, OnReconnect fires first and the
// clk.After call happens AFTER Advance, so the deadline is registered
// against the already-advanced now and the test deadlocks on a channel
// that will never receive.
//
// The assertion runs inside the OnReconnect callback itself so the
// observation is synchronous with the sync point tests use: if the
// FakeClock has at least one pending deadline at callback time, the
// watcher has already registered the backoff and subsequent Advance
// calls are deterministic.
func TestReconnect_RegistersBackoffDeadlineBeforeOnReconnect(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, _ := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	var (
		pendingAtCallback atomic.Int64
		callbackFired     = make(chan struct{}, 1)
	)

	opts := attachOptionsWithBackoff()

	opts.OnReconnect = func(_ int, _ error) {
		pendingAtCallback.Store(int64(clk.Pending()))

		select {
		case callbackFired <- struct{}{}:
		default:
		}
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	select {
	case <-callbackFired:
	case <-time.After(reconnectTestSettleBudget):
		t.Fatal("OnReconnect did not fire")
	}

	require.GreaterOrEqual(t, pendingAtCallback.Load(), int64(1),
		"FakeClock must have the backoff deadline registered BEFORE OnReconnect fires")
}
