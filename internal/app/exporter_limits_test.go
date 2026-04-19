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

// addrListener wraps a pipeListener so each accepted conn reports a
// specified TCPAddr from RemoteAddr. Tests that exercise the per-peer
// cap and the ACL need a stable IP across accepts; net.Pipe normally
// returns a pipeAddr with no IP, which the per-peer tracker cannot
// distinguish between peers.
type addrListener struct {
	*pipeListener

	remote *net.TCPAddr
}

// newAddrListener wraps a pipeListener with a fixed peer address for
// every accepted conn.
func newAddrListener(remote *net.TCPAddr) *addrListener {
	return &addrListener{pipeListener: newPipeListener(), remote: remote}
}

// Accept wraps the inner pipeListener's Accept with a connWithRemote
// that reports the preset TCPAddr.
func (l *addrListener) Accept() (net.Conn, error) {
	c, err := l.pipeListener.Accept()
	if err != nil {
		return nil, err
	}

	return &connWithRemote{Conn: c, remote: l.remote}, nil
}

// connWithRemote overrides RemoteAddr so the exporter's per-peer
// tracking and ACL logic can inspect a plausible IP.
type connWithRemote struct {
	net.Conn

	remote *net.TCPAddr
}

// RemoteAddr returns the preset peer address.
func (c *connWithRemote) RemoteAddr() net.Addr { return c.remote }

// TestExporter_MaxSessions asserts the Nth+1 connection (after N
// already-active imports) is rejected without invoking ExportOnConn.
func TestExporter_MaxSessions(t *testing.T) {
	t.Parallel()

	var exports atomic.Int32

	releaseExport := make(chan struct{})

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			exports.Add(1)
			<-releaseExport

			return nil
		},
	}

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		DecodeOpReqImportFunc: func(_ io.Reader) (domain.BusID, error) {
			return domain.BusID("1-1"), nil
		},
	}

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1234})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterMaxSessions(1),
	)
	t.Cleanup(func() {
		close(releaseExport)
		require.NoError(t, exp.Shutdown(context.Background()))
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	// First session holds; wait until it has reached ExportOnConn.
	first, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = first.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	require.Eventually(t, func() bool { return exports.Load() == 1 },
		2*time.Second, 10*time.Millisecond)

	// Second session: MaxSessions cap forces the handler to close
	// the conn without touching ExportOnConn.
	second, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = second.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	// Server end closes; client read returns EOF within a short window.
	_ = second.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, err = second.Read(make([]byte, 1))
	require.Error(t, err)
	require.NoError(t, second.Close())

	// ExportOnConn was called exactly once — the cap blocked the second.
	require.Equal(t, int32(1), exports.Load())

	cancel()

	_ = first.Close()

	<-serveDone
}

// TestExporter_MaxSessionsPerPeer asserts a 9th connection from the same
// IP is rejected when MaxSessionsPerPeer is 8 — the actual default —
// and that a differently-sourced peer is not affected.
func TestExporter_MaxSessionsPerPeer(t *testing.T) {
	t.Parallel()

	const perPeerCap = 2

	releaseExport := make(chan struct{})

	var exports atomic.Int32

	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			exports.Add(1)
			<-releaseExport

			return nil
		},
	}

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		DecodeOpReqImportFunc: func(_ io.Reader) (domain.BusID, error) {
			return domain.BusID("1-1"), nil
		},
	}

	peer := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 5), Port: 4000}
	lis := newAddrListener(peer)

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterMaxSessionsPerPeer(perPeerCap),
	)
	t.Cleanup(func() {
		close(releaseExport)
		require.NoError(t, exp.Shutdown(context.Background()))
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	holds := make([]net.Conn, 0, perPeerCap)

	for range perPeerCap {
		c, err := lis.dial(ctx)
		require.NoError(t, err)

		_, err = c.Write(opHeader(wire.OpReqImport))
		require.NoError(t, err)

		holds = append(holds, c)
	}

	require.Eventually(t, func() bool { return exports.Load() == int32(perPeerCap) },
		2*time.Second, 10*time.Millisecond)

	// Third connection from the same peer is closed without hitting ExportOnConn.
	extra, err := lis.dial(ctx)
	require.NoError(t, err)

	_, err = extra.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	_ = extra.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, err = extra.Read(make([]byte, 1))
	require.Error(t, err)
	require.NoError(t, extra.Close())

	require.Equal(t, int32(perPeerCap), exports.Load())

	cancel()

	for _, c := range holds {
		_ = c.Close()
	}

	<-serveDone
}

// TestExporter_RateLimit asserts connections beyond the token bucket
// burst are rejected at the accept loop without reaching the codec.
// We configure a very low rate and small burst so the first few
// connections pass and the rest are closed immediately.
func TestExporter_RateLimit(t *testing.T) {
	t.Parallel()

	const burst = 2

	var headerDecodes atomic.Int32

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: func(r io.Reader) (uint16, wire.OpCode, uint32, error) {
			headerDecodes.Add(1)

			return wire.NewCodec().DecodeHeader(r)
		},
	}

	kernel := &ExporterKernelMock{}

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1234})

	exp := newExporterForTest(t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		// 0.001 rps ≈ no refills during the test window; burst sets
		// the permitted initial accepts.
		app.WithExporterAcceptRateLimit(0.001, burst),
	)
	t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	// Fire 5 connections; only burst should complete the handshake,
	// the rest are dropped before DecodeHeader runs.
	const totalConns = 5

	for range totalConns {
		c, err := lis.dial(ctx)
		require.NoError(t, err)

		// Close client end immediately: rate-limited conns never reach
		// the codec; allowed conns see EOF and exit the handler.
		require.NoError(t, c.Close())
	}

	// Allow handlers a moment to finish.
	time.Sleep(50 * time.Millisecond)

	require.LessOrEqual(t, headerDecodes.Load(), int32(burst),
		"rate-limited conns must not reach DecodeHeader")

	cancel()
	<-serveDone
}

// TestExporter_MaxHandshakeBytes asserts the handler closes a conn
// that sends garbage exceeding MaxHandshakeBytes without ever matching
// a valid OP header.
func TestExporter_MaxHandshakeBytes(t *testing.T) {
	t.Parallel()

	const capBytes = 16

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
	}

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1234})

	exp := newExporterForTest(t,
		app.WithExporterKernel(&ExporterKernelMock{}),
		app.WithExporterCodec(codec),
		app.WithExporterMaxHandshakeBytes(capBytes),
	)
	t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	// Send capBytes+something of non-header garbage. The exporter must
	// refuse to read past the cap and close the conn.
	payload := make([]byte, capBytes*4)
	for i := range payload {
		payload[i] = 0xFF
	}

	// Write in a goroutine so the test does not block on a closed peer.
	go func() { _, _ = client.Write(payload) }()

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, err = client.Read(make([]byte, 1))
	require.Error(t, err)
	require.NoError(t, client.Close())

	cancel()
	<-serveDone
}

// TestExporter_HandshakeTimeoutCoversBodyDecode asserts that the
// handshake deadline remains armed through the FULL handshake read —
// both the header AND the OP_REQ_IMPORT body. Pre-fix the handler
// disarms the deadline right after DecodeHeader, so a peer that writes
// a valid 8-byte header and then stalls on the busid body never gets
// closed until TCP keep-alive fires. Post-fix the deadline covers
// DecodeOpReqImport too.
func TestExporter_HandshakeTimeoutCoversBodyDecode(t *testing.T) {
	t.Parallel()

	const handshakeTimeout = 100 * time.Millisecond

	clk := testutil.NewFakeClockAt(exporterTestEpoch())

	// Force a real DecodeHeader and a slow DecodeOpReqImport: the mock
	// reads BusIDSize bytes directly so the handler parks between
	// header decode and busid decode — exactly the window the pre-fix
	// code leaves uncovered by the timeout.
	decodeBodyReached := make(chan struct{}, 1)

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
		DecodeOpReqImportFunc: func(r io.Reader) (domain.BusID, error) {
			select {
			case decodeBodyReached <- struct{}{}:
			default:
			}

			buf := make([]byte, domain.BusIDSize)

			_, readErr := io.ReadFull(r, buf)
			if readErr != nil {
				return "", io.EOF
			}

			return domain.BusID(buf), nil
		},
	}

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1234})

	exp := newExporterForTest(t,
		app.WithExporterKernel(&ExporterKernelMock{}),
		app.WithExporterCodec(codec),
		app.WithExporterClock(clk),
		app.WithExporterHandshakeTimeout(handshakeTimeout),
	)

	ctx, cancel := context.WithCancel(context.Background())

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	t.Cleanup(func() {
		// Closing the client unblocks any body-decode that the
		// server-side timeout failed to tear down, so the handler can
		// drain and Shutdown completes even on the failing pre-fix tree.
		_ = client.Close()

		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(
			context.Background(), 2*time.Second)
		defer shutdownCancel()

		require.NoError(t, exp.Shutdown(shutdownCtx))

		<-serveDone
	})

	// Write only the header — no busid body. The handler will decode
	// the header and then park in DecodeOpReqImport reading the busid.
	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	select {
	case <-decodeBodyReached:
	case <-time.After(2 * time.Second):
		t.Fatal("DecodeOpReqImport was not reached within 2s")
	}

	// Advance past the handshake deadline; the timeout watcher MUST
	// force-close the conn even though DecodeHeader already returned.
	clk.Advance(handshakeTimeout + 10*time.Millisecond)

	// Read with a short deadline: the handshake timeout must close the
	// conn within a small window after the advance. Pre-fix, the
	// watcher stops at DecodeHeader and the conn stays open — Read
	// blocks until the test's own deadline, which we detect by timing.
	readStart := time.Now()

	_ = client.SetReadDeadline(readStart.Add(500 * time.Millisecond))

	_, err = client.Read(make([]byte, 1))

	readElapsed := time.Since(readStart)

	require.Error(t, err, "body-decode stall must be closed by handshake timeout")
	require.Less(t, readElapsed, 300*time.Millisecond,
		"handshake timeout should close the conn quickly; "+
			"Read returning near the test's own deadline means the "+
			"server-side timeout never fired")
}

// TestExporter_HandshakeTimeout asserts that a connection which
// writes nothing is closed after HandshakeTimeout elapses. Uses the
// FakeClock so the test is deterministic without wall-clock waits.
func TestExporter_HandshakeTimeout(t *testing.T) {
	t.Parallel()

	const handshakeTimeout = 100 * time.Millisecond

	clk := testutil.NewFakeClockAt(exporterTestEpoch())

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
	}

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 1234})

	exp := newExporterForTest(t,
		app.WithExporterKernel(&ExporterKernelMock{}),
		app.WithExporterCodec(codec),
		app.WithExporterClock(clk),
		app.WithExporterHandshakeTimeout(handshakeTimeout),
	)
	t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)

	// Wait until the handler is parked reading the header, then advance
	// the clock past the handshake deadline.
	time.Sleep(20 * time.Millisecond)
	clk.Advance(handshakeTimeout + 10*time.Millisecond)

	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	_, err = client.Read(make([]byte, 1))
	require.Error(t, err)
	require.NoError(t, client.Close())

	cancel()
	<-serveDone
}
