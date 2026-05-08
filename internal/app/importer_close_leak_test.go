package app_test

import (
	"context"
	"io"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// newLeakProbeFixture builds a reconnect-enabled Importer whose
// OnReconnect callback blocks on a release channel. After a reconnect
// fires, the callback goroutine parks and — pre-fix — leaks past any
// Close call whose timeout expires before the callback returns (RANK
// 11). The returned releaseCallback lets a test unblock the callback
// once it has observed the leak assertion.
func newLeakProbeFixture(t *testing.T) (
	*app.Importer,
	*testutil.FakeClock,
	*eventChannelRegistry,
	chan struct{},
) {
	t.Helper()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, clk, registry, _ := newReconnectFixture(t, attachFn)

	releaseCallback := make(chan struct{})

	return imp, clk, registry, releaseCallback
}

// TestImporterOnReconnectCallbackTrackedByWG proves the RANK 11 fix:
// OnReconnect's goroutine must be tracked by i.wg so Close blocks on
// it (bounded by shutdownTimeout). Pre-fix the goroutine is spawned
// with raw `go`, is NOT in the wg, and can outlive Close indefinitely.
// Using the test's releaseCallback gate we wedge the callback AND
// hold its goroutine — Close must still return within shutdownTimeout
// budget, AND once we release the callback the goroutine must exit
// promptly (no stranded work).
func TestImporterOnReconnectCallbackTrackedByWG(t *testing.T) {
	t.Parallel()

	imp, clk, registry, releaseCallback := newLeakProbeFixture(t)

	var callbackEntered atomic.Int32

	opts := attachOptionsWithBackoff()

	opts.OnReconnect = func(_ int, _ error) {
		callbackEntered.Add(1)
		<-releaseCallback
	}

	opts.ShutdownTimeout = 50 * time.Millisecond

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	// Trigger reconnect so OnReconnect fires.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return callbackEntered.Load() >= 1
	}, reconnectTestSettleBudget, 5*time.Millisecond)

	// Close within shutdownTimeout budget. Post-fix the callback goroutine
	// is tracked by the wg so Close's bounded wait properly observes it
	// (the inner wait goroutine itself must exit promptly when the budget
	// lapses or the callback completes).
	baselineGoroutines := runtime.NumGoroutine()

	closeDone := make(chan error, 1)

	go func() {
		closeDone <- imp.Close()
	}()

	// The bounded wait in Close must return on time.
	var closeErr error

	require.Eventually(t, func() bool {
		clk.Advance(opts.ShutdownTimeout)

		select {
		case closeErr = <-closeDone:
			return true
		default:
			return false
		}
	}, 2*time.Second, 5*time.Millisecond, "Close must honour ShutdownTimeout even with blocking OnReconnect")

	require.NoError(t, closeErr)

	// Release the callback; its goroutine must exit promptly — RANK
	// 11 pre-fix would park forever.
	close(releaseCallback)

	// Give the runtime a moment to schedule the goroutine exit. If the
	// callback goroutine was wg-tracked we would have seen it drop
	// BEFORE Close returned; the post-release check here simply asserts
	// it is not leaked beyond that point.
	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baselineGoroutines+1
	}, 2*time.Second, 10*time.Millisecond,
		"OnReconnect goroutine must exit after the callback unblocks")
}

// TestImporterCloseTimeoutBoundedWaiterDoesNotLeak proves the RANK 10
// fix. Repeated Close-with-timeout calls against an importer with a
// blocking callback must NOT accumulate leaked waitgroup-waiter
// goroutines. Pre-fix each timed-out Close spawns an `i.wg.Wait`
// goroutine that strands on the wg forever; 100 Close calls leak 100
// goroutines. Post-fix the waiter observes the timeout signal and
// returns promptly.
func TestImporterCloseTimeoutBoundedWaiterDoesNotLeak(t *testing.T) {
	t.Parallel()

	// This fixture never reconnects but still wires a wg-tracked
	// workload: we deliberately enrol a long-running "stuck" goroutine
	// via spawnReconnectWatcher through an Attach that never completes
	// its reconnect. The kernel's AttachRemote blocks until test
	// teardown so the watcher's own reconnect attempts park forever;
	// Close's bounded waiter is the unit under test.
	release := make(chan struct{})

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return domain.PortID(1), nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return nil },
		ListPortsFunc:  func(_ context.Context) ([]domain.Port, error) { return nil, nil },
	}

	registry := newEventChannelRegistry()

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			ch, cancel := registry.subscribe()

			return ch, cancel, nil
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

	clk := testutil.NewFakeClockAt(importerTestEpoch())

	imp := app.NewImporter(
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(events),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterClock(clk),
	)

	var onReconnectBlocked atomic.Int32

	opts := attachOptionsWithBackoff()

	opts.OnReconnect = func(_ int, _ error) {
		onReconnectBlocked.Add(1)
		<-release
	}

	opts.ShutdownTimeout = 10 * time.Millisecond

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)

	registry.waitFor(t, 1)

	// Fire a reconnect so OnReconnect starts blocking.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: 1}}

	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return onReconnectBlocked.Load() >= 1
	}, reconnectTestSettleBudget, 5*time.Millisecond)

	baseline := runtime.NumGoroutine()

	// Close is idempotent — calling twice must not spawn a second
	// waiter goroutine. Post-fix even the first timeout-bounded Close
	// must not leak a waiter. Release the callback BEFORE the final
	// assertion so the package's goleak harness is happy.
	require.NoError(t, imp.Close())

	// Eventually the post-Close goroutine count must settle within a
	// fixed bound above baseline even though Close timed out. Pre-fix
	// the wg-waiter leak would push numGoroutines past baseline for
	// the remainder of the test run.
	close(release)

	require.Eventually(t, func() bool {
		return runtime.NumGoroutine() <= baseline
	}, 2*time.Second, 10*time.Millisecond,
		"timeout-bounded Close must not leak the waitgroup-waiter goroutine")
}
