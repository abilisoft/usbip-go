package app_test

import (
	"context"
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

	// The watcher spawned by the reconnect attempt subscribes too, so
	// waiting for channel index 1 synchronises with the successful
	// re-attach without sleeping.
	registry.waitFor(t, 2)

	clk.Advance(reconnectTestBackoff().Delay)

	require.Eventually(t, func() bool {
		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond, "AttachRemote should be called exactly twice after reconnect")
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

	// Advance by the poll interval twice — once to move past the Used
	// observation, once to trigger the StatusNull detection.
	clk.Advance(opts.StatusPollInterval)
	clk.Advance(opts.StatusPollInterval)

	registry.waitFor(t, 2)

	clk.Advance(reconnectTestBackoff().Delay)

	require.Eventually(t, func() bool {
		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond, "poll-detected detach must produce exactly one reconnect")
}

// TestImporterReconnectBackoffRespected asserts the watcher sleeps for
// exactly Backoff.Next(attempt) between attempts. With FixedBackoff and
// FakeClock we can verify by refusing to advance: no reconnect occurs
// until the clock ticks forward by the configured delay.
func TestImporterReconnectBackoffRespected(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

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

	// Three reconnect attempts, each gated by the fixed backoff.
	for range 3 {
		require.Eventually(t, func() bool {
			// Advance the clock each iteration so a pending After fires.
			clk.Advance(reconnectTestBackoff().Delay)

			return false
		}, 50*time.Millisecond, 10*time.Millisecond)
	}

	// Close waits on the Importer waitgroup — if the watcher is still
	// looping, Close will never return. That failure mode is the
	// assertion: the watcher must exit after MaxAttempts.
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

	registry.waitFor(t, 2)
	clk.Advance(reconnectTestBackoff().Delay)

	require.Eventually(t, func() bool {
		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond)

	// New watcher is now subscribed on registry channel index 1 and
	// tracks port id 2. A stale event for port id 1 MUST NOT cause a
	// third AttachRemote call.
	registry.channel(t, 1) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Never(t, func() bool {
		return len(kernel.AttachRemoteCalls()) > 2
	}, 50*time.Millisecond, 5*time.Millisecond, "stale old-port-id event must be ignored by the new watcher")
}
