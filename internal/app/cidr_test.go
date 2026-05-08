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

// TestExporterACL_Allow asserts a peer in the allow-list completes the
// handshake through to DecodeHeader.
func TestExporterACL_Allow(t *testing.T) {
	t.Parallel()

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		EncodeOpRepDevlistFunc: func(_ io.Writer, _ []domain.Device) error {
			return nil
		},
	}

	kernel := &ExporterKernelMock{
		ListLocalDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return nil, nil
		},
	}

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 1234})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterACL("10.0.0.0/24"),
	)
	t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = client.Write(opHeader(wire.OpReqDevlist))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(codec.DecodeHeaderCalls()) >= 1
	}, 2*time.Second, 10*time.Millisecond,
		"allowed peer must reach DecodeHeader")

	cancel()

	_ = client.Close()

	<-serveDone
}

// TestExporterACL_Reject asserts a peer outside every allowed CIDR is
// closed at accept time without ever reaching DecodeHeader.
func TestExporterACL_Reject(t *testing.T) {
	t.Parallel()

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
	}

	kernel := &ExporterKernelMock{}

	// Peer is outside the allow-list.
	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(192, 168, 1, 5), Port: 1234})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterACL("10.0.0.0/24"),
	)
	t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, err = client.Read(make([]byte, 1))
	require.Error(t, err)
	require.NoError(t, client.Close())

	// Give the accept handler a moment to run (rejected path); then
	// assert the codec was never reached.
	time.Sleep(50 * time.Millisecond)

	require.Empty(t, codec.DecodeHeaderCalls(),
		"rejected peer must not reach the codec")

	cancel()
	<-serveDone
}

// TestExporterACL_InvalidCIDR_RejectsAtConstruction asserts a bogus
// allow-list entry surfaces as a constructor error rather than a
// deferred failure at Serve time.
func TestExporterACL_InvalidCIDR_RejectsAtConstruction(t *testing.T) {
	t.Parallel()

	_, err := app.NewExporterWithError(
		app.WithExporterKernel(&ExporterKernelMock{}),
		app.WithExporterEvents(&KernelEventsMock{}),
		app.WithExporterTransport(&TransportMock{}),
		app.WithExporterCodec(&ProtocolCodecMock{}),
		app.WithExporterACL("bogus"),
	)

	require.Error(t, err)
	require.ErrorIs(t, err, app.ErrACLInvalid)
}

// TestExporterACL_ServeContinuesAfterReject asserts Serve keeps
// accepting peers after one is rejected by the ACL. A single rejection
// must not cause the accept loop to exit.
func TestExporterACL_ServeContinuesAfterReject(t *testing.T) {
	t.Parallel()

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
	}

	kernel := &ExporterKernelMock{}

	badListener := newAddrListener(&net.TCPAddr{IP: net.IPv4(192, 168, 1, 5), Port: 1234})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterACL("10.0.0.0/24"),
	)
	t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, badListener) }()

	c1, err := badListener.dial(ctx)
	require.NoError(t, err)
	require.NoError(t, c1.Close())

	time.Sleep(50 * time.Millisecond)

	select {
	case <-serveDone:
		t.Fatal("Serve exited after ACL rejection")
	default:
	}

	cancel()
	<-serveDone
}
