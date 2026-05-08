package app_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestExporterShutdownHonoursConfiguredTimeout proves the RANK 9 fix.
// WithExporterShutdownTimeout on the internal Exporter must bound a
// Shutdown(ctx) whose ctx has no deadline: the option is the last-
// resort backstop against a wedged ExportOnConn. Pre-RANK-9 the
// public option existed but stored only on the public config and
// NEVER plumbed into the internal Shutdown path, so operators relying
// on the option saw Shutdown block indefinitely.
func TestExporterShutdownHonoursConfiguredTimeout(t *testing.T) {
	t.Parallel()

	exportStarted := make(chan struct{}, 1)

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, c net.Conn, _ domain.BusID) error {
			select {
			case exportStarted <- struct{}{}:
			default:
			}

			_, _ = io.Copy(io.Discard, c)

			return io.EOF
		},
		// Pass-2 RANK 3: Shutdown now issues a graceful kernel
		// Disconnect before the bounded drain. The fixture needs a
		// stub even when the scenario exercises the backstop path
		// (kernel ignores Disconnect and Shutdown must still bound).
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		DecodeOpReqImportFunc: func(_ io.Reader) (domain.BusID, error) {
			return domain.BusID("9-9"), nil
		},
	}

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 2000})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterShutdownTimeout(50*time.Millisecond),
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
		t.Fatal("ExportOnConn never started")
	}

	// Shutdown with an UNBOUNDED ctx. The only deadline in scope is
	// the one wired via WithExporterShutdownTimeout — if the option is
	// ignored this call blocks until the enclosing test times out.
	start := time.Now()

	err = exp.Shutdown(context.Background())

	require.Less(t, time.Since(start), 2*time.Second,
		"Shutdown(ctx-without-deadline) must honour WithExporterShutdownTimeout")
	require.Error(t, err,
		"Shutdown must surface the DeadlineExceeded-class error when the configured timeout fires")

	cancel()

	<-serveDone
}
