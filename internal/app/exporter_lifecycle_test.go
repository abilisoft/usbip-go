package app_test

import (
	"context"
	"io"
	"net"
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
func newSessionImportCodec(busID domain.BusID) *ProtocolCodecMock {
	return &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		DecodeOpReqImportFunc: func(_ io.Reader) (domain.BusID, error) {
			return busID, nil
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
		// Pass-2 RANK 3: fixture helper needs a DisconnectFunc so the
		// cleanup-time Shutdown does not panic the mock.
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
// set of accepted sessions. Mirrors spec §5.3's `Sessions(ctx)` contract.
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
	// deltas per spec §3.4.
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
	// waitForSessionEnd helper (RANK 1). In the real kernel the analogue
	// of the release-channel is the kernel detach uevent; here the test
	// uses ctx cancel since the default KernelEvents mock never delivers.
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
// Pre-fix: Shutdown closes handle.done but the handler is parked in
// ExportOnConn with no signal path, so Shutdown times out AND the
// session stays live after Shutdown returns (and the waitSessionsBounded
// bg goroutine leaks). Post-fix: Shutdown force-closes every tracked
// session conn so ExportOnConn errors out and the handler drains.
func TestExporterShutdown_DeadlineExceededForcesConnClose(t *testing.T) {
	t.Parallel()

	exportStarted := make(chan struct{}, 1)

	kernel := &ExporterKernelMock{
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
		// Pass-2 RANK 3: the deadline-exceeded fixture deliberately
		// models a kernel that never unwinds Disconnect gracefully.
		// The stub returns nil without emitting an event so the
		// bounded drain falls through to the force-close path.
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
	// session bookkeeping entry is gone. Without force-close the handler
	// is still parked in ExportOnConn and Sessions() still reports 1.
	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 0
	}, 500*time.Millisecond, 10*time.Millisecond,
		"Shutdown must drive the stuck handler to exit")

	cancel()

	<-serveDone
}

// TestExporterShutdown_ReturnsEvenWhenHandlerIgnoresClose locks in
// the Pass-4 RANK 2 fix: Shutdown's drain must be bounded by the
// caller ctx deadline even when a handler is truly stuck (ignores
// conn.Close() and never unwinds). Pre-fix waitSessionsBounded does
// `<-done` unbounded after forceCloseSessionConns, so a handler that
// never returns parks Shutdown forever.
//
// Post-fix: Shutdown returns within a small grace after the ctx
// deadline with a ctx.DeadlineExceeded-wrapped error; the truly stuck
// handler goroutine is accepted as a leak (logged) but does not block
// the Shutdown return.
func TestExporterShutdown_ReturnsEvenWhenHandlerIgnoresClose(t *testing.T) {
	t.Parallel()

	exportStarted := make(chan struct{}, 1)

	// hang is NEVER closed by the test (only unblocked in t.Cleanup so
	// the test goroutine itself does not leak past the test run).
	hang := make(chan struct{})

	t.Cleanup(func() { close(hang) })

	kernel := &ExporterKernelMock{
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

// TestExporterWatchSessions_AfterShutdown asserts WatchSessions after
// Shutdown returns an empty iter that terminates immediately. Matches
// the Importer.Watch post-Close contract per spec §3.4.
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
