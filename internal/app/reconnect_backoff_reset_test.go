// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

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

// countingBackoff is a BackoffStrategy test double that records every
// Next and Reset invocation. Zero Delay — the watcher skips the
// FakeClock sleep via the zero-delay branch in waitBackoff, keeping
// the test deterministic without clock advances.
type countingBackoff struct {
	next  atomic.Int32
	reset atomic.Int32
}

// Next increments the counter and returns zero so the watcher's
// waitBackoff short-circuits without exercising the FakeClock.
func (c *countingBackoff) Next(_ int) time.Duration {
	c.next.Add(1)

	return 0
}

// Reset records that Reset was called.
func (c *countingBackoff) Reset() { c.reset.Add(1) }

// NextCalls returns the current Next-invocation count.
func (c *countingBackoff) NextCalls() int32 { return c.next.Load() }

// ResetCalls returns the current Reset-invocation count.
func (c *countingBackoff) ResetCalls() int32 { return c.reset.Load() }

// TestReconnect_ResetsBackoffAfterSuccess locks in the v1 contract §5.5
// Backoff.Reset contract (internal/app/backoff.go:20 — "Reset is
// called after a successful reconnect so the next failure starts
// from the smallest delay again"). If finishReconnectSuccess did not
// invoke Reset on the injected strategy, a stateful backoff (custom
// BackoffStrategy that carries per-outage state) would stay escalated
// across outages and the next failure would pay the last-attempt
// delay instead of the floor.
func TestReconnect_ResetsBackoffAfterSuccess(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, _, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	backoff := &countingBackoff{}

	opts := app.AttachOptions{
		AutoReconnect:      true,
		Backoff:            backoff,
		StatusPollInterval: -1,
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	// Drive a detach. The reconnect succeeds on attempt 1 (attachFn
	// always returns nil). Per contract, Reset must fire once on the
	// success branch in finishReconnectSuccess.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"reconnect must succeed on attempt 1")

	// Reset is called on the original watcher goroutine after the
	// recursive Attach returns; the replacement watcher's subscription
	// runs on a separate goroutine spawned inside that Attach, so its
	// arrival in the registry races with the Reset call. Poll directly
	// on the counter rather than treating the subscribe count as a
	// happens-before fence.
	require.Eventually(t, func() bool {
		return backoff.ResetCalls() == 1
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"Backoff.Reset must fire exactly once after the reconnect succeeds")
}
