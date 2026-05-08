package app_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestImporterReconnectStuckWatcher_DoesNotSilentlyReattachAfterDetach
// asserts that a reconnect watcher wedged inside kernel.AttachRemote
// cannot register a fresh handle after Detach has already bounded-waited
// for it and returned success (RANK 3). Pre-fix, the sequence was:
//
//  1. Attach with AutoReconnect succeeds; watcher spawned.
//  2. Detach uevent drives the watcher into its reconnect loop; the
//     next kernel.AttachRemote blocks (the test's release channel
//     simulates a slow kernel).
//  3. User calls Detach with a tight ShutdownTimeout; the bounded wait
//     on watcherDone fires, Detach issues kernel.DetachPort and returns
//     nil.
//  4. The stuck AttachRemote finally returns a fresh PortID — post-fix
//     the watcher must observe the detaching flag set by Detach and
//     roll back the kernel handoff; pre-fix it registered the new
//     handle and the device silently reappeared.
//
// Success criteria: after the sequence settles, (a) no handle exists
// for the busid, (b) kernel.DetachPort was invoked for both the
// original port AND the rollback port, (c) Sessions() returns empty.
func TestImporterReconnectStuckWatcher_DoesNotSilentlyReattachAfterDetach(t *testing.T) {
	t.Parallel()

	var (
		attachCount  atomic.Int32
		release      = make(chan struct{})
		detachedIDs  = make(chan domain.PortID, 4)
	)

	// First AttachRemote (initial Attach) returns a port immediately.
	// The second call — the reconnect attempt after the detach uevent
	// — blocks on `release`. When the test closes release, it returns
	// a fresh PortID 2, which pre-fix lands in the handle map.
	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		n := attachCount.Add(1)
		if n == 1 {
			return domain.PortID(1), nil
		}

		<-release

		return domain.PortID(2), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)

	// Custom DetachPort that records every port id it was invoked for
	// so the assertions can confirm both the original AND the rollback
	// detach fired.
	kernel.DetachPortFunc = func(_ context.Context, id domain.PortID) error {
		detachedIDs <- id

		return nil
	}

	t.Cleanup(func() {
		// Release the stuck attach AFTER the Detach bounded wait has
		// already timed out. This is the load-bearing interleave.
		select {
		case <-release:
		default:
			close(release)
		}

		require.NoError(t, imp.Close())
	})

	opts := attachOptionsWithBackoff()

	// Tight ShutdownTimeout so Detach proceeds past the wedged watcher
	// without real wall-clock waits.
	opts.ShutdownTimeout = 50 * time.Millisecond

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	// Feed the detach uevent; the watcher enters its reconnect loop.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	// Spin the clock until the watcher is parked inside the second
	// AttachRemote call.
	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return attachCount.Load() >= 2
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"reconnect attempt must enter AttachRemote before Detach is issued")

	// User-initiated Detach with the wedged watcher. Pre-fix this
	// returns nil, removes the handle, but the stuck AttachRemote is
	// still about to succeed and register a fresh handle.
	detachDone := make(chan error, 1)

	go func() {
		detachDone <- imp.Detach(context.Background(), port.ID)
	}()

	// Advance the fake clock past the ShutdownTimeout so the bounded
	// wait's timer fires and Detach proceeds to kernel.DetachPort.
	require.Eventually(t, func() bool {
		clk.Advance(opts.ShutdownTimeout)

		select {
		case <-detachDone:
			return true
		default:
			return false
		}
	}, 1*time.Second, 5*time.Millisecond,
		"Detach must return within ShutdownTimeout despite wedged watcher")

	// First DetachPort call records PortID=1 from the user Detach path.
	select {
	case got := <-detachedIDs:
		require.Equal(t, domain.PortID(1), got, "Detach must release the original port")
	case <-time.After(1 * time.Second):
		t.Fatal("kernel.DetachPort was not invoked for the original port")
	}

	// Now release the stuck AttachRemote — it returns PortID 2.
	// Post-fix: the watcher observes handle.detaching and rolls back
	// via a DetachPort(2) call; no handle ends up in the map.
	// Pre-fix: the watcher registers PortID 2 and the device silently
	// reappears.
	close(release)

	// Second DetachPort call records PortID=2 from the rollback.
	select {
	case got := <-detachedIDs:
		require.Equal(t, domain.PortID(2), got,
			"watcher must roll back the successful-after-Detach attach")
	case <-time.After(1 * time.Second):
		t.Fatal("rollback kernel.DetachPort(2) was not invoked — watcher silently reattached")
	}

	// The user-facing view: ListPorts must stay empty; Sessions-like
	// invariant — no handle for the busid.
	require.Eventually(t, func() bool {
		ports, perr := imp.ListPorts(context.Background())
		require.NoError(t, perr)

		return len(ports) == 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"no handle must remain after wedged-watcher rollback")
}
