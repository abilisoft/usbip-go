// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"io"
	"net"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// sessionImportCodec is the minimal codec configuration needed to drive
// one OP_REQ_IMPORT through serveImport. Tests that exercise the
// session lifecycle share this setup via newSessionImportCodec.
//
// serveImport now sends OP_REP_IMPORT before kernel handoff — the mock
// must accept both the success-reply encode call and the error-reply
// encode call (the latter fires on subscribe failure / busid collision /
// register decline paths).
func newSessionImportCodec(busID domain.BusID) *ProtocolCodecMock {
	return &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		DecodeOpReqImportBodyFunc: func(_ io.Reader) (domain.BusID, error) {
			return busID, nil
		},
		EncodeOpRepImportFunc: func(_ io.Writer, _ domain.Device) error {
			return nil
		},
		EncodeOpRepImportErrorFunc: func(_ io.Writer, _ uint32) error {
			return nil
		},
	}
}

// startExporterImportSession helps tests bring up a single long-lived
// import session. Returns the running exporter, the client conn, a
// func that releases ExportOnConn, and the serveDone channel.
func startExporterImportSession(
	t *testing.T,
) (*app.Exporter, net.Conn, chan<- struct{}, <-chan error, context.CancelFunc) {
	t.Helper()

	releaseExport := make(chan struct{})

	kernel := &ExporterKernelMock{
		// serveImport now looks the requested device up in the
		// exported set BEFORE sending OP_REP_IMPORT and handing the
		// fd to the kernel — return the busid so the lookup succeeds.
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: domain.BusID("3-1")}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, c net.Conn, _ domain.BusID) error {
			// Watch both the test-driven release AND the conn itself so a
			// Shutdown force-close on drain-exceed unwedges the handler
			// instead of deadlocking on releaseExport.
			closedCh := make(chan struct{})

			go func() {
				defer close(closedCh)

				_, _ = c.Read(make([]byte, 1))
			}()

			select {
			case <-releaseExport:
				_ = c.Close()

				<-closedCh
			case <-closedCh:
			}

			return nil
		},
		// Fixture helper needs a DisconnectFunc so the cleanup-time
		// Shutdown does not panic the mock.
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	codec := newSessionImportCodec(domain.BusID("3-1"))

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 9000})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	return exp, client, releaseExport, serveDone, cancel
}

// TestExporterSessions_ReflectsCurrent asserts Sessions() returns the
// set of accepted sessions. Mirrors v1 contract §5.3's `Sessions(ctx)` contract.
func TestExporterSessions_ReflectsCurrent(t *testing.T) {
	t.Parallel()

	exp, client, release, serveDone, cancel := startExporterImportSession(t)

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond,
		"Sessions() must reflect the accepted import")

	got := exp.Sessions(context.Background())
	require.Len(t, got, 1)
	require.Equal(t, domain.BusID("3-1"), got[0].BusID)

	close(release)
	cancel()

	_ = client.Close()

	require.NoError(t, exp.Shutdown(context.Background()))

	<-serveDone
}

// TestExporterWatchSessions_StartEnd asserts WatchSessions yields
// SessionStartedEvent and SessionEndedEvent for a full session
// lifecycle.
func TestExporterWatchSessions_StartEnd(t *testing.T) {
	t.Parallel()

	exp, client, release, serveDone, cancel := startExporterImportSession(t)

	// Subscribe BEFORE releasing export so we see SessionStartedEvent,
	// but AFTER the session is accepted so the Started event path has
	// already published to the internal queue — consumers see future
	// deltas per v1 contract §3.4.
	watchCtx, watchCancel := context.WithCancel(context.Background())
	t.Cleanup(watchCancel)

	// Subscribe SYNCHRONOUSLY on the test goroutine so the subscriber
	// is registered before we drive session-end — otherwise the
	// in-goroutine subscribe can lose the race to close(release) and
	// miss SessionEndedEvent. Iterate the returned seq in a worker.
	seq := exp.WatchSessions(watchCtx)

	evs := make(chan domain.Event, 8)

	go func() {
		for ev := range seq {
			select {
			case evs <- ev:
			case <-watchCtx.Done():
				return
			}
		}
	}()

	// Give the watcher a moment to subscribe; session end happens when
	// release fires and the outer ctx cancel unwinds the post-ExportOnConn
	// waitForSessionEnd helper. In the real kernel the analogue of the
	// release-channel is the kernel detach uevent; here the test uses
	// ctx cancel since the default KernelEvents mock never delivers.
	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	close(release)
	cancel()

	// Expect a SessionEndedEvent after the handler unwinds.
	var gotEnded bool

	deadline := time.After(2 * time.Second)

waitEnded:
	for !gotEnded {
		select {
		case ev := <-evs:
			if _, ok := ev.(domain.SessionEndedEvent); ok {
				gotEnded = true
			}
		case <-deadline:
			break waitEnded
		}
	}

	require.True(t, gotEnded, "expected a SessionEndedEvent within 2s")

	_ = client.Close()

	require.NoError(t, exp.Shutdown(context.Background()))

	<-serveDone
}

// TestExporterShutdown_Drains asserts Shutdown drains an in-flight
// session when the kernel's session-end arrives before the deadline.
func TestExporterShutdown_Drains(t *testing.T) {
	t.Parallel()

	exp, client, release, serveDone, cancel := startExporterImportSession(t)

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	// Spawn a goroutine that simulates the kernel ending the session
	// shortly after Shutdown starts draining.
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(release)
	}()

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 2*time.Second)
	defer shutdownCancel()

	require.NoError(t, exp.Shutdown(shutdownCtx))

	cancel()

	_ = client.Close()

	<-serveDone
}

// TestExporterShutdown_DeadlineExceeded asserts Shutdown returns a
// ctx.Err-wrapped error when the drain deadline expires before the
// session ends.
func TestExporterShutdown_DeadlineExceeded(t *testing.T) {
	t.Parallel()

	exp, client, release, serveDone, cancel := startExporterImportSession(t)
	t.Cleanup(func() {
		close(release)
		cancel()

		_ = client.Close()

		<-serveDone
	})

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 50*time.Millisecond)
	defer shutdownCancel()

	err := exp.Shutdown(shutdownCtx)
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestExporterShutdown_DeadlineExceededForcesConnClose asserts that
// Shutdown unwedges a session whose kernel.ExportOnConn blocks forever.
// Shutdown force-closes every tracked session conn so ExportOnConn
// errors out and the handler drains; without that the session would
// stay live after Shutdown returns (and waitSessionsBounded's bg
// goroutine would leak).
func TestExporterShutdown_DeadlineExceededForcesConnClose(t *testing.T) {
	t.Parallel()

	exportStarted := make(chan struct{}, 1)

	kernel := &ExporterKernelMock{
		// serveImport now looks the requested device up in the
		// exported set BEFORE sending OP_REP_IMPORT and handing the
		// fd to the kernel — return the busid so the lookup succeeds.
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: domain.BusID("3-1")}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, c net.Conn, _ domain.BusID) error {
			select {
			case exportStarted <- struct{}{}:
			default:
			}

			// Block until the conn gets closed by Shutdown's force-close.
			// Returning io.EOF keeps wrapcheck happy without re-wrapping
			// the net error surface.
			_, _ = io.Copy(io.Discard, c)

			return io.EOF
		},
		// The deadline-exceeded fixture deliberately models a kernel
		// that never unwinds Disconnect gracefully. The stub returns
		// nil without emitting an event so the bounded drain falls
		// through to the force-close path.
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	codec := newSessionImportCodec(domain.BusID("3-1"))

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 9000})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
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
	case <-exportStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("ExportOnConn did not begin blocking within 2s")
	}

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 50*time.Millisecond)
	defer shutdownCancel()

	_ = exp.Shutdown(shutdownCtx)

	// Post-Shutdown: the session handler must have drained, i.e. the
	// session bookkeeping entry is gone. Without force-close the
	// handler would still be parked in ExportOnConn and Sessions()
	// would still report 1.
	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"Shutdown must drive the stuck handler to exit")

	cancel()

	<-serveDone
}

// TestExporterShutdown_ReturnsEvenWhenHandlerIgnoresClose pins the
// bounded-drain contract: Shutdown's drain must be bounded by the
// caller ctx deadline even when a handler is truly stuck (ignores
// conn.Close() and never unwinds). If waitSessionsBounded did an
// unbounded <-done after forceCloseSessionConns, a handler that never
// returns would park Shutdown forever.
//
// Shutdown returns within a small grace after the ctx deadline with a
// ctx.DeadlineExceeded-wrapped error; the truly stuck handler
// goroutine is accepted as a leak (logged) but does not block the
// Shutdown return.
func TestExporterShutdown_ReturnsEvenWhenHandlerIgnoresClose(t *testing.T) {
	t.Parallel()

	exportStarted := make(chan struct{}, 1)

	// hang is NEVER closed by the test (only unblocked in t.Cleanup so
	// the test goroutine itself does not leak past the test run).
	hang := make(chan struct{})

	t.Cleanup(func() { close(hang) })

	kernel := &ExporterKernelMock{
		// serveImport now looks the requested device up in the
		// exported set BEFORE sending OP_REP_IMPORT and handing the
		// fd to the kernel — return the busid so the lookup succeeds.
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: domain.BusID("3-1")}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			select {
			case exportStarted <- struct{}{}:
			default:
			}

			// Handler deliberately ignores conn.Close — it waits only on
			// an independent channel the test never closes during the
			// Shutdown window. Returning io.EOF keeps wrapcheck quiet
			// after the test-cleanup release.
			<-hang

			return io.EOF
		},
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	codec := newSessionImportCodec(domain.BusID("3-1"))

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 9000})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
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
	case <-exportStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("ExportOnConn did not begin blocking within 2s")
	}

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(
		context.Background(), 100*time.Millisecond)
	defer shutdownCancel()

	shutdownDone := make(chan error, 1)

	go func() { shutdownDone <- exp.Shutdown(shutdownCtx) }()

	var shutdownErr error

	select {
	case shutdownErr = <-shutdownDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not return within 2s despite 100ms ctx; " +
			"waitSessionsBounded is blocking on `<-done` after force-close")
	}

	require.Error(t, shutdownErr)
	require.ErrorIs(t, shutdownErr, context.DeadlineExceeded)
}

// TestExporterWatchSessions_DoesNotRegisterUntilIterated pins the
// lazy-registration contract for WatchSessions: constructing the
// returned iter MUST NOT occupy a slot in the Exporter's fanout list
// until the consumer ranges over it. A caller that discards the iter
// would otherwise leak a buffered channel and a slice slot until
// Shutdown's closeAllSubscribers fires. Mirrors the same audit
// finding fixed for Importer.Watch.
func TestExporterWatchSessions_DoesNotRegisterUntilIterated(t *testing.T) {
	t.Parallel()

	exp := newExporterForTest(t)
	t.Cleanup(func() { _ = exp.Shutdown(context.Background()) })

	require.Zero(t, app.SessionSubscribersLenForTest(exp))

	_ = exp.WatchSessions(context.Background())

	require.Zero(t, app.SessionSubscribersLenForTest(exp),
		"WatchSessions must defer subscriber registration until the iter is ranged over")
}

// TestExporterWatchSessions_AfterShutdown asserts WatchSessions after
// Shutdown returns an empty iter that terminates immediately. Matches
// the Importer.Watch post-Close contract per v1 contract §3.4.
func TestExporterWatchSessions_AfterShutdown(t *testing.T) {
	t.Parallel()

	exp := newExporterForTest(t)

	require.NoError(t, exp.Shutdown(context.Background()))

	var count atomic.Int32

	for range exp.WatchSessions(context.Background()) {
		count.Add(1)
	}

	require.Equal(t, int32(0), count.Load())
}

// stableNumGoroutines reads runtime.NumGoroutine several times with a
// small sleep between samples and returns the minimum. Shutdown-driven
// cleanup churn (timers firing, wg.Wait returning) produces transient
// goroutine spikes; the min across samples is a stable floor we can
// compare pre vs post without flakes from unrelated runtime activity.
func stableNumGoroutines(t *testing.T) int {
	t.Helper()

	const (
		samples = 10
		pause   = 5 * time.Millisecond
	)

	best := runtime.NumGoroutine()

	for range samples - 1 {
		time.Sleep(pause)

		n := runtime.NumGoroutine()
		if n < best {
			best = n
		}
	}

	return best
}

// TestExporterShutdown_ReusesSessionsWaitGoroutine pins the shared-
// drain-future contract: repeated Shutdown calls on a truly-stuck
// handler must not leak one `go e.sessionsWG.Wait()` waiter per call.
//
// If waitSessionsBounded did `go func() { e.sessionsWG.Wait(); close(done) }()`
// on every invocation, a parked handler would never return from Wait()
// and every Shutdown call would add one more permanently parked
// goroutine (N extra Shutdown calls => +N goroutines that never
// unwind).
//
// The drain future is shared (sync.Once-guarded) so only the first
// Shutdown spawns a waiter; subsequent calls observe the same shared
// channel. N extra Shutdown calls => at most +1 goroutine.
func TestExporterShutdown_ReusesSessionsWaitGoroutine(t *testing.T) {
	t.Parallel()

	exportStarted := make(chan struct{}, 1)
	hang := make(chan struct{})

	t.Cleanup(func() { close(hang) })

	kernel := &ExporterKernelMock{
		// serveImport now looks the requested device up in the
		// exported set BEFORE sending OP_REP_IMPORT and handing the
		// fd to the kernel — return the busid so the lookup succeeds.
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: domain.BusID("3-1")}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			select {
			case exportStarted <- struct{}{}:
			default:
			}

			<-hang

			return io.EOF
		},
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	codec := newSessionImportCodec(domain.BusID("3-1"))

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 9000})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
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
	case <-exportStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("ExportOnConn did not begin blocking within 2s")
	}

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	// One priming Shutdown to kick off the (possibly shared) waiter;
	// its timer-goroutines then settle out before we measure.
	primeCtx, primeCancel := context.WithTimeout(
		context.Background(), 100*time.Millisecond)

	_ = exp.Shutdown(primeCtx)

	primeCancel()

	// Baseline after the first Shutdown has finished its transient
	// bookkeeping.
	baseline := stableNumGoroutines(t)

	const extraShutdowns = 5

	for range extraShutdowns {
		shutdownCtx, shutdownCancel := context.WithTimeout(
			context.Background(), 50*time.Millisecond)

		_ = exp.Shutdown(shutdownCtx)

		shutdownCancel()
	}

	after := stableNumGoroutines(t)

	delta := after - baseline

	// Allow a small slack for unrelated scheduler noise (pprof, gc,
	// net poller). A regression that leaked one waiter per call would
	// push delta >= extraShutdowns; the shared drain future keeps
	// delta <= 1.
	const maxDelta = 1

	require.LessOrEqualf(t, delta, maxDelta,
		"repeated Shutdown leaked waiter goroutines: baseline=%d after=%d delta=%d extraShutdowns=%d",
		baseline, after, delta, extraShutdowns)
}

// TestExporterShutdown_RecoversFromSpuriousDeadlineRace pins the
// non-blocking re-check invariant: when the shared drain future is
// ALREADY closed and the caller ctx is already cancelled,
// waitSessionsBounded's first select has two ready cases. Go's select
// picks randomly among ready cases, so without a non-blocking re-check
// of `done` after ctx.Done is observed, Shutdown would return a
// spurious context.Canceled wrap roughly 50% of the time even though
// the drain completed.
//
// Trigger: on each iteration, construct an Exporter, call Shutdown
// once to prime the drain future, then call Shutdown a SECOND time
// with an already-cancelled ctx. By the second call the shared drain
// future is already closed from the first Shutdown; the ctx is also
// already cancelled. Both select cases are ready simultaneously. The
// non-blocking re-check deterministically returns nil; without it a
// substantial fraction of iterations out of 100 would surface the
// spurious error
//
//	"exporter shutdown: context canceled"
func TestExporterShutdown_RecoversFromSpuriousDeadlineRace(t *testing.T) {
	t.Parallel()

	const iterations = 100

	for i := range iterations {
		exp := newExporterForTest(t)

		// Prime the shared drain future so `done` is closed by the
		// time the racing Shutdown runs. With zero sessions the
		// spawned waiter's sessionsWG.Wait() returns immediately and
		// closes sessionsDrained; the first Shutdown then blocks until
		// observing that close via `<-done`.
		require.NoError(t, exp.Shutdown(context.Background()),
			"priming Shutdown must complete cleanly")

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := exp.Shutdown(ctx)
		require.NoErrorf(t, err,
			"iteration %d: Shutdown must observe completed drain even when ctx is already cancelled",
			i)
	}
}
