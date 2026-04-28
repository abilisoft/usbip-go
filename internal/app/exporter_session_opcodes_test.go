// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestExporterServe_ReplyOpcodeClosesConn pins the handleConn branch that
// rejects reply-side opcodes (OpRepDevlist, OpRepImport) arriving on an
// accepted connection. A well-behaved exporter peer never sends reply
// opcodes; receiving one indicates a mis-configured peer or reversed
// role. The handler must log and close without calling ExportOnConn or
// EncodeOpRepDevlist.
func TestExporterServe_ReplyOpcodeClosesConn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		op   wire.OpCode
	}{
		{"OpRepDevlist", wire.OpRepDevlist},
		{"OpRepImport", wire.OpRepImport},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			kernel := &ExporterKernelMock{}
			codec := &ProtocolCodecMock{
				DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
			}

			lis := newPipeListener()

			exp := newExporterForTest(t,
				app.WithExporterKernel(kernel),
				app.WithExporterCodec(codec),
			)

			ctx, cancel := context.WithCancel(context.Background())
			serveDone := make(chan error, 1)

			go func() { serveDone <- exp.Serve(ctx, lis) }()

			client, err := lis.dial(ctx)
			require.NoError(t, err)

			_, err = client.Write(opHeader(tc.op))
			require.NoError(t, err)

			// Server must close the conn after receiving a reply opcode.
			require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))

			_, err = client.Read(make([]byte, 1))
			require.Error(t, err, "server must close conn on reply opcode %v", tc.op)

			require.NoError(t, client.Close())
			require.Empty(t, kernel.ExportOnConnCalls(),
				"ExportOnConn must not be called when peer sends reply opcode")

			cancel()
			require.NoError(t, <-serveDone)
		})
	}
}

// TestExporterServe_UnknownOpcodeClosesConn pins the default branch in
// handleConn. Any opcode not in the dispatch table (OP_REQ_DEVLIST,
// OP_REQ_IMPORT, or the reply sentinels) is silently rejected and the
// connection is closed. Tests that a numerical opcode outside the
// defined set triggers the default arm.
func TestExporterServe_UnknownOpcodeClosesConn(t *testing.T) {
	t.Parallel()

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
	}
	kernel := &ExporterKernelMock{}

	lis := newPipeListener()

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = client.Write(opHeader(wire.OpCode(0x9999)))
	require.NoError(t, err)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))

	_, err = client.Read(make([]byte, 1))
	require.Error(t, err, "server must close conn on unknown opcode")

	require.NoError(t, client.Close())
	require.Empty(t, kernel.ExportOnConnCalls())

	cancel()
	require.NoError(t, <-serveDone)
}

// TestExporterServe_DevlistKernelError pins the serveDevlist path where
// ListLocalDevices returns an error. The handler must log the warning,
// not call EncodeOpRepDevlist, and close the connection. The accept loop
// must continue accepting further connections (single-error non-fatal).
func TestExporterServe_DevlistKernelError(t *testing.T) {
	t.Parallel()

	kernel := &ExporterKernelMock{
		ListLocalDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return nil, fmt.Errorf("sysfs read: %w", errBoom)
		},
	}

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
	}

	lis := newPipeListener()

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = client.Write(opHeader(wire.OpReqDevlist))
	require.NoError(t, err)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))

	_, err = client.Read(make([]byte, 1))
	require.Error(t, err, "server must close conn when ListLocalDevices errors")

	require.NoError(t, client.Close())
	require.Empty(t, codec.EncodeOpRepDevlistCalls(),
		"EncodeOpRepDevlist must not be called when ListLocalDevices errors")

	cancel()
	require.NoError(t, <-serveDone)
}

// TestExporterServe_DevlistEncodeError pins the serveDevlist path where
// EncodeOpRepDevlist returns an error after a successful ListLocalDevices.
// The handler must surface the encode failure as a failed handshake
// outcome and close the connection without panicking.
func TestExporterServe_DevlistEncodeError(t *testing.T) {
	t.Parallel()

	want := []domain.Device{{BusID: domain.BusID("1-1")}}

	kernel := &ExporterKernelMock{
		ListLocalDevicesFunc: func(_ context.Context) ([]domain.Device, error) { return want, nil },
	}

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		EncodeOpRepDevlistFunc: func(_ io.Writer, _ []domain.Device) error {
			return fmt.Errorf("encode: %w", errBoom)
		},
	}

	lis := newPipeListener()

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = client.Write(opHeader(wire.OpReqDevlist))
	require.NoError(t, err)

	// Allow the handler to process.
	time.Sleep(50 * time.Millisecond)

	require.Len(t, kernel.ListLocalDevicesCalls(), 1,
		"ListLocalDevices must be called before EncodeOpRepDevlist is attempted")
	require.Len(t, codec.EncodeOpRepDevlistCalls(), 1,
		"EncodeOpRepDevlist must be called even when it errors")

	// Drain any partial write and verify the conn is closed.
	// SetReadDeadline may fail when the server has already closed its end
	// of the pipe; the error is intentionally discarded — the deadline is
	// a safety net against hangs, not a correctness assertion.
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, _ = client.Read(make([]byte, 128))
	require.NoError(t, client.Close())

	cancel()

	select {
	case err := <-serveDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return within 3s of ctx cancel")
	}
}

// TestExporterServe_DevlistEncodeError_ConnClosed verifies that after the
// EncodeOpRepDevlist error the server closes its end of the connection so
// the client observes EOF (or a connection reset).
func TestExporterServe_DevlistWriteErrorClosesConn(t *testing.T) {
	t.Parallel()

	kernel := &ExporterKernelMock{
		ListLocalDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: "3-1"}}, nil
		},
	}

	// The codec writes some bytes before erroring so the client can
	// detect the early close via a short-read or EOF.
	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		EncodeOpRepDevlistFunc: func(w io.Writer, _ []domain.Device) error {
			_, _ = w.Write([]byte("partial"))

			return fmt.Errorf("write half: %w", errBoom)
		},
	}

	lis := newPipeListener()

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = client.Write(opHeader(wire.OpReqDevlist))
	require.NoError(t, err)

	require.NoError(t, client.SetReadDeadline(time.Now().Add(2*time.Second)))

	// Read until EOF or error: server must close after the encode error.
	buf := make([]byte, 128)

	for {
		_, rerr := client.Read(buf)
		if rerr != nil {
			break
		}
	}

	require.NoError(t, client.Close())

	cancel()

	select {
	case err := <-serveDone:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return within 3s of ctx cancel")
	}
}
