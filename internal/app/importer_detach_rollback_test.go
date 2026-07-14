// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"fmt"
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

type observedReconnectClock struct {
	base  *testutil.FakeClock
	calls chan time.Duration
}

func newObservedReconnectClock() *observedReconnectClock {
	return &observedReconnectClock{
		base:  testutil.NewFakeClockAt(importerTestEpoch()),
		calls: make(chan time.Duration, 16),
	}
}

func (c *observedReconnectClock) Now() time.Time {
	return c.base.Now()
}

func (c *observedReconnectClock) Sleep(d time.Duration) {
	c.base.Sleep(d)
}

func (c *observedReconnectClock) After(d time.Duration) <-chan time.Time {
	result := c.base.After(d)
	c.calls <- d

	return result
}

func (c *observedReconnectClock) waitForAfter(t *testing.T, want time.Duration) {
	t.Helper()

	for {
		select {
		case got := <-c.calls:
			if got == want {
				return
			}
		case <-time.After(detachCoordinationTimeout):
			t.Fatalf("timed out waiting for Clock.After(%s)", want)
		}
	}
}

func requireEventChannelClosed(t *testing.T, events <-chan domain.Event) {
	t.Helper()

	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-time.After(detachCoordinationTimeout):
			t.Fatal("timed out waiting for reconnect watcher event subscription to close")
		}
	}
}

// TestImporterReconnectStuckWatcher_DoesNotSilentlyReattachAfterDetach
// asserts that a reconnect watcher wedged inside kernel.AttachRemote
// cannot register a fresh handle after Detach has already bounded-waited
// for it and returned success. The sequence is:
//
//  1. Attach with AutoReconnect succeeds; watcher spawned.
//  2. Detach uevent drives the watcher into its reconnect loop; the
//     next kernel.AttachRemote blocks (the test's release channel
//     simulates a slow kernel).
//  3. User calls Detach with a tight ShutdownTimeout; the bounded wait
//     on watcherDone fires, Detach issues kernel.DetachPort and returns
//     nil.
//  4. The stuck AttachRemote finally returns a fresh PortID — the
//     watcher must observe the detaching flag set by Detach and roll
//     back the kernel handoff so the device does not silently reappear.
//
// Success criteria: after the sequence settles, (a) no handle exists
// for the busid, (b) kernel.DetachPort was invoked for both the
// original port AND the rollback port, (c) Sessions() returns empty.
func TestImporterReconnectStuckWatcher_DoesNotSilentlyReattachAfterDetach(t *testing.T) {
	t.Parallel()

	var (
		attachCount atomic.Int32
		release     = make(chan struct{})
		detachedIDs = make(chan domain.PortID, 4)
	)

	// First AttachRemote (initial Attach) returns a port immediately.
	// The second call — the reconnect attempt after the detach uevent
	// — blocks on `release`. When the test closes release, it returns
	// a fresh PortID 2, which without the rollback guard would land in
	// the handle map.
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

	// User-initiated Detach with the wedged watcher. Detach must return
	// nil and remove the handle even though the stuck AttachRemote is
	// still about to succeed and would otherwise register a fresh
	// handle.
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
	// The watcher must observe handle.detaching and roll back via a
	// DetachPort(2) call so no handle ends up in the map and the
	// device does not silently reappear.
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

func TestImporterReconnectRollbackSharesReplacementCompensation(t *testing.T) {
	t.Parallel()

	const (
		originalPort    domain.PortID = 21
		replacementPort domain.PortID = 22
		shutdownTimeout               = 3 * time.Second
	)

	var attachCalls atomic.Int32

	replacementReserved := make(chan struct{})
	releaseReplacement := make(chan struct{})
	replacementDetachEntered := make(chan struct{})
	releaseReplacementDetach := make(chan struct{})
	originalDetached := make(chan struct{})

	attachFn := func(
		_ context.Context, _ net.Conn, spec app.RemoteDeviceSpec,
	) (domain.PortID, error) {
		if attachCalls.Add(1) == 1 {
			reserveErr := spec.ReserveLocalPort(originalPort)
			if reserveErr != nil {
				return 0, fmt.Errorf("reserve original port: %w", reserveErr)
			}

			return originalPort, nil
		}

		reserveErr := spec.ReserveLocalPort(replacementPort)
		if reserveErr != nil {
			return 0, fmt.Errorf("reserve replacement port: %w", reserveErr)
		}

		close(replacementReserved)
		<-releaseReplacement

		return replacementPort, nil
	}

	registry := newEventChannelRegistry()
	clock := newObservedReconnectClock()
	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc:     attachFn,
		ListPortsFunc:        func(_ context.Context) ([]domain.Port, error) { return nil, nil },
	}
	kernelEvents := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			ch, cancel := registry.subscribe()

			return ch, cancel, nil
		},
	}
	transport := &TransportMock{
		DialFunc: func(
			_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions,
		) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) {
			return attachDevice(), nil
		},
	}
	imp := newImporterForTest(
		t,
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(kernelEvents),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterClock(clock),
	)

	var (
		replacementDetachCalls atomic.Int32
		originalDetachOnce     sync.Once
	)

	kernel.DetachPortFunc = func(_ context.Context, id domain.PortID) error {
		switch id {
		case originalPort:
			originalDetachOnce.Do(func() { close(originalDetached) })
		case replacementPort:
			if replacementDetachCalls.Add(1) == 1 {
				close(replacementDetachEntered)
				<-releaseReplacementDetach
			}
		}

		return nil
	}

	t.Cleanup(func() {
		select {
		case <-releaseReplacement:
		default:
			close(releaseReplacement)
		}

		select {
		case <-releaseReplacementDetach:
		default:
			close(releaseReplacementDetach)
		}

		require.NoError(t, imp.Close())
	})

	backoffReady := make(chan struct{})
	opts := attachOptionsWithBackoff()

	opts.ShutdownTimeout = shutdownTimeout
	opts.OnReconnect = func(_ int, _ error) { close(backoffReady) }

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, originalPort, port.ID)

	registry.waitFor(t, 1)

	originalEvents := registry.channel(t, 0)
	originalEvents <- domain.PortDetachedEvent{Port: domain.Port{ID: originalPort}}

	requireSignal(t, backoffReady, "reconnect backoff registration")
	clock.waitForAfter(t, reconnectTestBackoff().Delay)
	clock.base.Advance(reconnectTestBackoff().Delay)
	requireSignal(t, replacementReserved, "replacement port reservation")

	originalDetachDone := make(chan error, 1)
	go func() { originalDetachDone <- imp.Detach(context.Background(), originalPort) }()

	clock.waitForAfter(t, shutdownTimeout)
	clock.base.Advance(shutdownTimeout)
	requireSignal(t, originalDetached, "original port detach")
	require.NoError(t, <-originalDetachDone)

	replacementWaitCtx := newObservedDoneContext(context.Background())

	replacementDetachDone := make(chan error, 1)
	go func() {
		replacementDetachDone <- imp.Detach(replacementWaitCtx, replacementPort)
	}()

	requireSignal(t, replacementWaitCtx.doneObserved, "replacement publication wait")

	close(releaseReplacement)
	requireSignal(t, replacementDetachEntered, "replacement compensating detach")

	// The old watcher closes its subscription only after reaching rollback.
	// While the compensating detach is deliberately blocked, that proves
	// rollback observed the existing owner rather than racing a second kernel
	// mutation.
	requireEventChannelClosed(t, originalEvents)
	require.EqualValues(t, 1, replacementDetachCalls.Load())

	close(releaseReplacementDetach)
	require.NoError(t, <-replacementDetachDone)
}

func TestImporterSamePortReconnectReservationWinsOldDetach(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 23

	var attachCalls atomic.Int32

	replacementReserved := make(chan struct{})
	releaseReplacement := make(chan struct{})
	compensationEntered := make(chan struct{})

	attachFn := func(
		_ context.Context, _ net.Conn, spec app.RemoteDeviceSpec,
	) (domain.PortID, error) {
		reserveErr := spec.ReserveLocalPort(portID)
		if reserveErr != nil {
			return 0, fmt.Errorf("reserve local port: %w", reserveErr)
		}

		if attachCalls.Add(1) == 1 {
			return portID, nil
		}

		close(replacementReserved)
		<-releaseReplacement

		return portID, nil
	}

	imp, _, registry, kernel := newReconnectFixture(t, attachFn)

	var detachCalls atomic.Int32

	kernel.DetachPortFunc = func(_ context.Context, got domain.PortID) error {
		require.Equal(t, portID, got)

		if detachCalls.Add(1) == 1 {
			close(compensationEntered)

			return errSharedDetach
		}

		return nil
	}

	t.Cleanup(func() {
		select {
		case <-releaseReplacement:
		default:
			close(releaseReplacement)
		}

		require.NoError(t, imp.Close())
	})

	opts := attachOptionsWithBackoff()

	opts.Backoff = app.FixedBackoff{Delay: 0}
	opts.ShutdownTimeout = -1

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, portID, port.ID)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: portID}}

	requireSignal(t, replacementReserved, "same-port replacement reservation")

	detachCtx := newObservedDoneContext(context.Background())
	detachDone := make(chan error, 1)

	go func() { detachDone <- imp.Detach(detachCtx, portID) }()

	requireSignal(t, detachCtx.doneObserved, "same-port publication wait")

	select {
	case <-compensationEntered:
		t.Fatal("old Detach reached kernel mutation before replacement publication")
	default:
	}

	close(releaseReplacement)
	requireSignal(t, compensationEntered, "same-port compensating detach")
	require.ErrorIs(t, <-detachDone, errSharedDetach)
	require.EqualValues(t, 1, detachCalls.Load(),
		"old generation must not issue a second detach behind replacement attach")

	require.NoError(t, imp.Detach(context.Background(), portID),
		"failed exact-generation compensation must retain the replacement for retry")
	require.EqualValues(t, 2, detachCalls.Load())

	require.NoError(t, imp.Close())
	require.EqualValues(t, 2, detachCalls.Load(),
		"Close after successful retry must not repeat the kernel mutation")
}

func TestImporterSamePortOldDetachRejectsLaterReservation(t *testing.T) {
	t.Parallel()

	const (
		portID          domain.PortID = 24
		shutdownTimeout               = 3 * time.Second
	)

	var (
		attachCalls atomic.Int32
		mutationMu  sync.Mutex
	)

	beforeReservation := make(chan struct{})
	releaseReservation := make(chan struct{})
	reservationRejected := make(chan struct{})
	detachAttempted := make(chan struct{})
	detachEntered := make(chan struct{})

	attachFn := func(
		_ context.Context, _ net.Conn, spec app.RemoteDeviceSpec,
	) (domain.PortID, error) {
		mutationMu.Lock()
		defer mutationMu.Unlock()

		if attachCalls.Add(1) == 1 {
			reserveErr := spec.ReserveLocalPort(portID)
			if reserveErr != nil {
				return 0, fmt.Errorf("reserve initial port: %w", reserveErr)
			}

			return portID, nil
		}

		close(beforeReservation)
		<-releaseReservation

		reserveErr := spec.ReserveLocalPort(portID)
		if reserveErr != nil {
			close(reservationRejected)

			return 0, fmt.Errorf("reserve replacement port: %w", reserveErr)
		}

		return portID, nil
	}

	clock := newObservedReconnectClock()
	imp, _, registry, kernel := newReconnectFixtureWithOptions(
		t, attachFn, app.WithImporterClock(clock),
	)

	kernel.DetachPortFunc = func(_ context.Context, got domain.PortID) error {
		close(detachAttempted)
		mutationMu.Lock()
		defer mutationMu.Unlock()

		require.Equal(t, portID, got)
		close(detachEntered)

		return nil
	}

	t.Cleanup(func() {
		select {
		case <-releaseReservation:
		default:
			close(releaseReservation)
		}

		require.NoError(t, imp.Close())
	})

	opts := attachOptionsWithBackoff()

	opts.Backoff = app.FixedBackoff{Delay: 0}
	opts.MaxAttempts = 1
	opts.ShutdownTimeout = shutdownTimeout

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), opts)
	require.NoError(t, err)
	require.Equal(t, portID, port.ID)

	registry.waitFor(t, 1)

	registry.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: portID}}

	requireSignal(t, beforeReservation, "pre-reservation replacement handoff")

	detachDone := make(chan error, 1)
	go func() { detachDone <- imp.Detach(context.Background(), portID) }()

	clock.waitForAfter(t, shutdownTimeout)
	clock.base.Advance(shutdownTimeout)
	requireSignal(t, detachAttempted, "old-generation kernel detach attempt")

	close(releaseReservation)
	requireSignal(t, reservationRejected, "detach-owned reservation rejection")
	requireSignal(t, detachEntered, "old-generation kernel detach")
	require.NoError(t, <-detachDone)
	require.EqualValues(t, 2, attachCalls.Load())
	require.Len(t, kernel.DetachPortCalls(), 1,
		"rejected replacement must not require rollback teardown")
}
