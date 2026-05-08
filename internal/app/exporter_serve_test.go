// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"encoding/binary"
	"fmt"
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

// pipeListener is a net.Listener backed by net.Pipe. Each Accept() call
// returns one end of a fresh synchronous pipe; the matching Dial()
// returns the other end. Close breaks the outstanding Accept so Serve
// can unwind. Mirrors the memnet-style listeners used across the Go
// ecosystem for transport-agnostic server tests.
type pipeListener struct {
	accept chan net.Conn
	closed chan struct{}
	closeN atomic.Int32
}

// newPipeListener returns a listener whose Accept returns conns
// delivered on the internal accept channel. Tests push client ends
// into dial() and consume server ends through Serve.
func newPipeListener() *pipeListener {
	return &pipeListener{
		accept: make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

// Accept blocks until dial pushes a server conn or the listener is
// closed.
func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.accept:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

// Close marks the listener closed; outstanding Accept calls return
// net.ErrClosed. Idempotent.
func (l *pipeListener) Close() error {
	if l.closeN.Add(1) == 1 {
		close(l.closed)
	}

	return nil
}

// Addr returns a synthetic pipe address. Tests that assert remote IP
// must wrap with a conn that reports a plausible TCPAddr instead.
func (*pipeListener) Addr() net.Addr { return fakeAddr{} }

// dial creates a net.Pipe and pushes the server end onto the accept
// channel; returns the client end so the test can drive the session.
// Returns an error when the listener has been closed.
func (l *pipeListener) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()

	select {
	case l.accept <- server:
		return client, nil
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()

		return nil, net.ErrClosed
	case <-ctx.Done():
		_ = client.Close()
		_ = server.Close()

		return nil, fmt.Errorf("pipe dial: %w", ctx.Err())
	}
}

// opHeader assembles an 8-byte USBIP OP header with the given opcode
// so tests can inject wire-level requests without depending on the
// wire package's Encode helpers (keeps the codec mock swap clean).
func opHeader(op wire.OpCode) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint16(buf[0:], domain.ProtocolVersion)
	binary.BigEndian.PutUint16(buf[2:], uint16(op))

	return buf
}

// drainN reads exactly n bytes from r, returning io.ErrUnexpectedEOF on
// short read. Tests use it to consume the Exporter's reply body.
func drainN(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)

	_, err := io.ReadFull(r, buf)
	if err != nil {
		return nil, fmt.Errorf("drainN: %w", err)
	}

	return buf, nil
}

// TestExporterServe_DevlistRequest drives a net.Pipe-backed Serve,
// sends an OP_REQ_DEVLIST over the client end, and asserts: the codec
// EncodeOpRepDevlist was called with the ListAvailable output, and the
// conn closed after the reply.
func TestExporterServe_DevlistRequest(t *testing.T) {
	t.Parallel()

	want := []domain.Device{
		{BusID: domain.BusID("1-1")},
		{BusID: domain.BusID("2-1")},
	}

	kernel := &ExporterKernelMock{
		ListLocalDevicesFunc: func(_ context.Context) ([]domain.Device, error) { return want, nil },
	}

	replyBody := []byte("REPLY")
	replyWritten := make(chan []domain.Device, 1)

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		EncodeOpRepDevlistFunc: func(w io.Writer, devs []domain.Device) error {
			_, err := w.Write(replyBody)
			if err != nil {
				return fmt.Errorf("write reply: %w", err)
			}

			replyWritten <- devs

			return nil
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

	got, err := drainN(client, len(replyBody))
	require.NoError(t, err)
	require.Equal(t, replyBody, got)

	// Server end should close; read returns EOF.
	_, err = client.Read(make([]byte, 1))
	require.ErrorIs(t, err, io.EOF)
	require.NoError(t, client.Close())

	select {
	case devs := <-replyWritten:
		require.Equal(t, want, devs)
	case <-time.After(2 * time.Second):
		t.Fatal("EncodeOpRepDevlist was not invoked")
	}

	cancel()
	require.NoError(t, <-serveDone)
}

// TestExporterServe_ImportHappyPath drives an OP_REQ_IMPORT through the
// session handler. The codec returns a busid; ExportOnConn is called
// with that busid; the handler blocks until the kernel signals session
// end via KernelEvents. The test asserts call order and that
// ExportOnConn received the same conn as was accepted.
func TestExporterServe_ImportHappyPath(t *testing.T) {
	t.Parallel()

	const importedBusID = domain.BusID("2-2")

	exported := make(chan struct{}, 1)
	releaseExport := make(chan struct{})

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, id domain.BusID) error {
			require.Equal(t, importedBusID, id)

			select {
			case exported <- struct{}{}:
			default:
			}

			<-releaseExport

			return nil
		},
	}

	// Mock the body-only decoder so it actually reads 32 bytes from
	// the reader, mirroring the real DecodeOpReqImportBody contract.
	// An inert mock that ignored its argument would let a regression
	// where the daemon double-reads the header (or skips the body)
	// pass silently.
	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		DecodeOpReqImportBodyFunc: func(r io.Reader) (domain.BusID, error) {
			body := make([]byte, domain.BusIDSize)

			_, rerr := io.ReadFull(r, body)
			if rerr != nil {
				return "", fmt.Errorf("decode op_req_import body: %w", rerr)
			}

			return importedBusID, nil
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

	// Send the full OP_REQ_IMPORT frame: 8-byte header + 32-byte busid
	// body. Without the body the body-only mock would block on
	// ReadFull and the test would observe a handshake timeout, not a
	// successful ExportOnConn.
	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	body := make([]byte, domain.BusIDSize)
	copy(body, importedBusID)

	_, err = client.Write(body)
	require.NoError(t, err)

	select {
	case <-exported:
	case <-time.After(2 * time.Second):
		t.Fatal("ExportOnConn was not invoked within 2s")
	}

	// Simulate session end: release the gate so ExportOnConn returns.
	close(releaseExport)
	cancel()

	_ = client.Close()

	require.NoError(t, <-serveDone)

	require.Len(t, kernel.ExportOnConnCalls(), 1)
	require.Equal(t, importedBusID, kernel.ExportOnConnCalls()[0].BusID)
}

// TestExporterServe_CtxCancellationStopsAccepting asserts that
// cancelling ctx closes the listener and Serve returns nil.
func TestExporterServe_CtxCancellationStopsAccepting(t *testing.T) {
	t.Parallel()

	lis := newPipeListener()

	exp := newExporterForTest(t)

	ctx, cancel := context.WithCancel(context.Background())

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	// Give Serve a moment to park on Accept.
	time.Sleep(10 * time.Millisecond)

	cancel()

	select {
	case err := <-serveDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of ctx cancel")
	}

	// Closed listener: any further dial is rejected.
	_, err := lis.dial(context.Background())
	require.ErrorIs(t, err, net.ErrClosed)
}

// TestExporterServe_PostShutdownReturnsError asserts Serve on an
// already-shutdown Exporter returns ErrAlreadyShutdown immediately.
func TestExporterServe_PostShutdownReturnsError(t *testing.T) {
	t.Parallel()

	exp := newExporterForTest(t)

	require.NoError(t, exp.Shutdown(context.Background()))

	err := exp.Serve(context.Background(), newPipeListener())
	require.ErrorIs(t, err, app.ErrAlreadyShutdown)
}
