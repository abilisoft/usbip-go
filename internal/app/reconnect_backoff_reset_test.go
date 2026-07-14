// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"log/slog"
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
	next              atomic.Int32
	reset             atomic.Int32
	secondBeforeReset atomic.Bool
}

// Next increments the counter and returns zero so the watcher's
// waitBackoff short-circuits without exercising the FakeClock.
func (c *countingBackoff) Next(_ int) time.Duration {
	call := c.next.Add(1)
	if call > 1 && c.reset.Load() == 0 {
		c.secondBeforeReset.Store(true)
	}

	return 0
}

// Reset records that Reset was called.
func (c *countingBackoff) Reset() { c.reset.Add(1) }

// NextCalls returns the current Next-invocation count.
func (c *countingBackoff) NextCalls() int32 { return c.next.Load() }

// ResetCalls returns the current Reset-invocation count.
func (c *countingBackoff) ResetCalls() int32 { return c.reset.Load() }

type blockingAttachLogHandler struct {
	attachCount atomic.Int32
	entered     chan struct{}
	release     chan struct{}
}

func (h *blockingAttachLogHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *blockingAttachLogHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message == "importer attached" && h.attachCount.Add(1) == 2 {
		close(h.entered)
		<-h.release
	}

	return nil
}

func (h *blockingAttachLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *blockingAttachLogHandler) WithGroup(string) slog.Handler      { return h }

// TestReconnect_ResetsBackoffAfterSuccess locks in the importer-lifecycle OpenSpec
// Backoff.Reset contract (internal/app/backoff.go:20 — "Reset is
// called after a successful reconnect so the next failure starts
// from the smallest delay again"). If finishReconnectSuccess did not
// invoke Reset on the injected strategy, a stateful backoff (custom
// BackoffStrategy that carries per-outage state) would stay escalated
// across outages and the next failure would pay the last-attempt delay
// instead of the floor. Reset now occurs during the successful reconnect
// Attach, before that Attach starts the replacement watcher.
func TestReconnect_ResetsBackoffAfterSuccess(t *testing.T) {
	t.Parallel()

	var nextID atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	imp, _, registry, kernel := newReconnectFixture(t, attachFn)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	backoff := &countingBackoff{}

	var factoryCalls atomic.Int32

	opts := app.AttachOptions{
		AutoReconnect: true,
		BackoffFactory: func() app.BackoffStrategy {
			factoryCalls.Add(1)

			return backoff
		},
		StatusPollInterval: -1,
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	// Drive a detach. The reconnect succeeds on attempt 1 (attachFn
	// always returns nil). Per contract, Reset must fire once on the
	// successful reconnect publication path.
	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"reconnect must succeed on attempt 1")

	require.Eventually(t, func() bool {
		return backoff.ResetCalls() == 1
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"Backoff.Reset must fire exactly once after the reconnect succeeds")
	require.EqualValues(t, 1, factoryCalls.Load(),
		"a replacement generation must retain the logical attachment's strategy")
}

func TestReconnect_ResetsBackoffBeforeReplacementWatcherNext(t *testing.T) {
	t.Parallel()

	var attachCalls atomic.Uint32

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		call := attachCalls.Add(1)
		return domain.PortID(call), nil
	}

	logHandler := &blockingAttachLogHandler{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	imp, _, registry, _ := newReconnectFixtureWithOptions(
		t, attachFn, app.WithImporterLogger(slog.New(logHandler)),
	)
	t.Cleanup(func() {
		select {
		case <-logHandler.release:
		default:
			close(logHandler.release)
		}

		require.NoError(t, imp.Close())
	})

	backoff := &countingBackoff{}
	opts := app.AttachOptions{
		AutoReconnect:      true,
		Backoff:            backoff,
		MaxAttempts:        1,
		StatusPollInterval: -1,
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	requireSignal(t, logHandler.entered, "replacement attach completion log")
	registry.waitFor(t, 2)

	registry.channel(t, 1) <- domain.PortDetachedEvent{Port: domain.Port{ID: 2}}

	require.Eventually(t, func() bool {
		return backoff.NextCalls() == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"replacement watcher must process the immediate second failure")

	require.False(t, backoff.secondBeforeReset.Load(),
		"replacement watcher called Next before the successful reconnect reset")
	require.EqualValues(t, 1, backoff.ResetCalls())
	require.EqualValues(t, 2, attachCalls.Load(),
		"the immediate second Attach must fail at in-flight dedup before kernel handoff")

	close(logHandler.release)
}
