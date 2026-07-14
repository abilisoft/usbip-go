// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// errRollbackDetachFailed is the mock sentinel returned by the
// kernel.DetachPort stub on the rollback call so the test can assert
// the failure path without reaching into slog output.
var errRollbackDetachFailed = errors.New("rollback detach failed (mock)")

// TestImporterRollback_PreservesHandleOnKernelDetachFailure pins the
// rollback contract. rollbackSupersededReconnect calls
// kernel.DetachPort FIRST and only deletes the handle entry on
// success. Deleting the handle entry before the kernel DetachPort
// succeeded would, on kernel failure, leave the kernel port live and
// owned by no handle: a subsequent explicit Detach(newID) from the
// user would return ErrDeviceNotBound and permanently orphan the port
// until a daemon restart.
//
// Test flow:
//  1. Initial Attach succeeds with PortID=1; watcher spawned.
//  2. Feed detach uevent; watcher enters reconnect loop, parks in
//     AttachRemote.
//  3. User Detach(1) with tight ShutdownTimeout proceeds past the
//     wedged watcher and succeeds.
//  4. Stuck AttachRemote returns PortID=2; rollback path invokes
//     kernel.DetachPort(2) which is rigged to fail.
//  5. The handle for PortID=2 MUST remain in i.handles. The user
//     explicit Detach(2) MUST reach the kernel (3rd DetachPort call)
//     and succeed this time — the handle was preserved.
func TestImporterRollback_PreservesHandleOnKernelDetachFailure(t *testing.T) {
	t.Parallel()

	var (
		attachCount atomic.Int32
		detachCount atomic.Int32
		release     = make(chan struct{})
		detachedIDs = make(chan domain.PortID, 4)
	)

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		n := attachCount.Add(1)
		if n == 1 {
			return domain.PortID(1), nil
		}

		<-release

		return domain.PortID(2), nil
	}

	imp, clk, registry, kernel := newReconnectFixture(t, attachFn)

	// DetachPort behaviour: record every invocation for assertion; the
	// rollback call (2nd invocation on PortID=2) fails deterministically,
	// the user's explicit-Detach retry (3rd invocation) succeeds.
	kernel.DetachPortFunc = func(_ context.Context, id domain.PortID) error {
		n := detachCount.Add(1)

		detachedIDs <- id

		if n == 2 {
			return errRollbackDetachFailed
		}

		return nil
	}

	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}

		require.NoError(t, imp.Close())
	})

	opts := attachOptionsWithBackoff()

	opts.ShutdownTimeout = 50 * time.Millisecond

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	originalEvents := registry.channel(t, 0)
	originalEvents <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return attachCount.Load() >= 2
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"reconnect attempt must enter AttachRemote before Detach is issued")

	detachDone := make(chan error, 1)

	go func() {
		detachDone <- imp.Detach(context.Background(), port.ID)
	}()

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

	// First DetachPort: user Detach(PortID=1). Succeeds per the mock.
	select {
	case got := <-detachedIDs:
		require.Equal(t, domain.PortID(1), got)
	case <-time.After(1 * time.Second):
		t.Fatal("kernel.DetachPort was not invoked for the original port")
	}

	// Release the stuck AttachRemote so the watcher's rollback path
	// fires with a failing kernel.DetachPort.
	close(release)

	// Second DetachPort: rollback on PortID=2. Mocked to fail.
	select {
	case got := <-detachedIDs:
		require.Equal(t, domain.PortID(2), got,
			"watcher must attempt rollback kernel.DetachPort(2) even if it fails")
	case <-time.After(1 * time.Second):
		t.Fatal("rollback kernel.DetachPort(2) was not invoked")
	}

	// The mock records the kernel call before it returns its failure. Wait for
	// the old watcher to close its subscription, which happens only after
	// rollback has published that failure and released shared-attempt
	// ownership; otherwise the explicit retry can legitimately join the still
	// active failed attempt instead of starting a new one.
	requireEventChannelClosed(t, originalEvents)

	// Because rollback DetachPort failed, the handle for PortID=2 MUST
	// stay registered so the user can retry. The retry Detach(2) must
	// reach the kernel — if the handle was deleted the retry returns
	// ErrDeviceNotBound and no DetachPort is issued.
	retryErr := imp.Detach(context.Background(), domain.PortID(2))
	require.NoError(t, retryErr,
		"retry Detach(2) must succeed — handle must remain registered "+
			"after rollback DetachPort failure so the kernel port can "+
			"still be released")

	select {
	case got := <-detachedIDs:
		require.Equal(t, domain.PortID(2), got,
			"retry must issue a fresh kernel.DetachPort(2) — proof the "+
				"handle was preserved and reachable via Detach")
	case <-time.After(1 * time.Second):
		t.Fatal("retry kernel.DetachPort(2) was not invoked — handle was not preserved")
	}
}
