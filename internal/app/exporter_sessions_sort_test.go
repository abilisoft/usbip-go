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
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestExporterSessions_SortStableOnEqualStartedAt asserts Sessions()
// returns a deterministic order when two sessions share an identical
// StartedAt timestamp. Pre-fix: sort.Slice tiebreaks arbitrarily, so
// repeated calls reshuffle the list. Post-fix: the sort tiebreaks by
// SessionID (UUIDv7 is lexical-time-ordered) so the order is stable.
func TestExporterSessions_SortStableOnEqualStartedAt(t *testing.T) {
	t.Parallel()

	// A FakeClock pinned to a single instant ensures buildSession
	// produces sessions with identical StartedAt values.
	clk := testutil.NewFakeClockAt(exporterTestEpoch())

	// Each session reads one byte of busid then parks until release so
	// the two imports co-exist in Sessions() for the duration of the
	// assertion loop.
	release := make(chan struct{})

	var imports atomic.Int32

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, c net.Conn, _ domain.BusID) error {
			imports.Add(1)

			closedCh := make(chan struct{})

			go func() {
				defer close(closedCh)

				_, _ = c.Read(make([]byte, 1))
			}()

			select {
			case <-release:
				_ = c.Close()

				<-closedCh
			case <-closedCh:
			}

			return nil
		},
	}

	// DecodeOpReqImport returns distinct busids per call so the two
	// sessions are not collapsed by any downstream dedup.
	var callN atomic.Int32

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		DecodeOpReqImportFunc: func(_ io.Reader) (domain.BusID, error) {
			n := callN.Add(1)

			if n == 1 {
				return domain.BusID("1-1"), nil
			}

			return domain.BusID("2-1"), nil
		},
	}

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 9100})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterClock(clk),
	)

	ctx, cancel := context.WithCancel(context.Background())

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	conns := make([]net.Conn, 0, 2)

	t.Cleanup(func() {
		close(release)
		cancel()

		for _, c := range conns {
			_ = c.Close()
		}

		shutdownCtx, shutdownCancel := context.WithTimeout(
			context.Background(), 2*time.Second)
		defer shutdownCancel()

		require.NoError(t, exp.Shutdown(shutdownCtx))

		<-serveDone
	})

	for range 2 {
		c, err := lis.dial(ctx)
		require.NoError(t, err)

		_, err = c.Write(opHeader(wire.OpReqImport))
		require.NoError(t, err)

		conns = append(conns, c)
	}

	require.Eventually(t, func() bool { return imports.Load() == 2 },
		2*time.Second, 10*time.Millisecond)

	// Capture the first snapshot as the baseline. Repeat 100 iterations
	// and assert every snapshot matches. Pre-fix, sort.Slice's
	// arbitrary tiebreak reshuffles the list under a load the scheduler
	// manifests quickly.
	first := exp.Sessions(context.Background())
	require.Len(t, first, 2)

	const iterations = 100

	for range iterations {
		got := exp.Sessions(context.Background())
		require.Equal(t, first, got,
			"Sessions() must return a deterministic order when "+
				"StartedAt values collide")
	}
}
