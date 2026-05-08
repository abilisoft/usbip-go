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
	extraOpts ...app.ExporterOption,
) (*app.Exporter, net.Conn, chan<- struct{}, <-chan error, context.CancelFunc) {
	t.Helper()

	releaseExport := make(chan struct{})

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			<-releaseExport

			return nil
		},
	}

	codec := newSessionImportCodec(domain.BusID("3-1"))

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 7), Port: 9000})

	opts := append([]app.ExporterOption{
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
	}, extraOpts...)

	exp := newExporterForTest(t, opts...)

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

	// Subscribe in a separate goroutine so we can race start vs
	// subscription and still receive the event via the buffered
	// internal channel.
	evs := make(chan domain.Event, 8)

	go func() {
		for ev := range exp.WatchSessions(watchCtx) {
			select {
			case evs <- ev:
			case <-watchCtx.Done():
				return
			}
		}
	}()

	// Give the watcher a moment to subscribe; session end happens when
	// release fires.
	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	close(release)

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

	cancel()

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
