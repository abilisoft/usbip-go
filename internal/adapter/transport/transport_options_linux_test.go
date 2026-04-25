// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package transport_test

import (
	"context"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/transport"
	"github.com/abilisoft/usbip-go/internal/netopts"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// dialOptsForBuffers exercises the SO_SNDBUF/SO_RCVBUF tuning path
// only; keepalive fields stay zero so the test does not race the
// keepalive assertions in the sibling test.
func dialOptsForBuffers() netopts.TransportOptions {
	return netopts.TransportOptions{
		SendBufferBytes:    256 * 1024,
		ReceiveBufferBytes: 256 * 1024,
	}
}

// rawSockoptInt fetches a SOL_SOCKET / IPPROTO_TCP int sockopt from
// the live TCP conn via SyscallConn. Returns the raw value the kernel
// reports — Linux doubles SO_SNDBUF/SO_RCVBUF internally so callers
// must compare with `>= requested`, not equality.
func rawSockoptInt(t *testing.T, conn net.Conn, level, opt int) int {
	t.Helper()

	tcpConn, ok := conn.(*net.TCPConn)
	require.True(t, ok, "expected *net.TCPConn, got %T", conn)

	rc, err := tcpConn.SyscallConn()
	require.NoError(t, err)

	var (
		val   int
		gerr  error
		ctlOK bool
	)

	ctlOK = rc.Control(func(fd uintptr) {
		val, gerr = syscall.GetsockoptInt(int(fd), level, opt)
	}) == nil

	require.True(t, ctlOK, "SyscallConn.Control failed")
	require.NoError(t, gerr)

	return val
}

// TestDialAppliesSocketBuffersLinux asserts that non-zero
// SendBufferBytes / ReceiveBufferBytes on TransportOptions reach the
// dialed TCP connection. Linux doubles the requested value internally
// (kernel ABI, see tcp(7) and socket(7)), so the assertion uses
// `actual >= requested` rather than equality.
func TestDialAppliesSocketBuffersLinux(t *testing.T) {
	t.Parallel()

	ln, ep := loopbackListener(t)

	defer func() { _ = ln.Close() }()

	accepted := acceptOnce(ln)

	tr := transport.New()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	opts := dialOptsForBuffers()

	conn, err := tr.Dial(ctx, ep, opts)
	require.NoError(t, err)

	defer func() { _ = conn.Close() }()

	rcv := rawSockoptInt(t, conn, syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	snd := rawSockoptInt(t, conn, syscall.SOL_SOCKET, syscall.SO_SNDBUF)

	require.GreaterOrEqual(t, rcv, opts.ReceiveBufferBytes,
		"SO_RCVBUF must be at least the requested size (Linux may double)")
	require.GreaterOrEqual(t, snd, opts.SendBufferBytes,
		"SO_SNDBUF must be at least the requested size (Linux may double)")

	srv := <-accepted
	require.NotNil(t, srv)

	_ = srv.Close()
}

// TestDialAppliesKeepAliveConfigLinux asserts that non-zero TCP
// keepalive fields (idle / interval / probes) reach the dialed TCP
// connection. Uses SetKeepAliveConfig on Linux (added in Go 1.23) so
// the per-connection knobs are observable via TCP_KEEPIDLE,
// TCP_KEEPINTVL, and TCP_KEEPCNT.
func TestDialAppliesKeepAliveConfigLinux(t *testing.T) {
	t.Parallel()

	ln, ep := loopbackListener(t)

	defer func() { _ = ln.Close() }()

	accepted := acceptOnce(ln)

	tr := transport.New()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	opts := netopts.TransportOptions{
		TCPKeepAliveIdle:     45 * time.Second,
		TCPKeepAliveInterval: 15 * time.Second,
		TCPKeepAliveProbes:   5,
	}

	conn, err := tr.Dial(ctx, ep, opts)
	require.NoError(t, err)

	defer func() { _ = conn.Close() }()

	keepalive := rawSockoptInt(t, conn, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE)
	require.NotZero(t, keepalive, "SO_KEEPALIVE must be enabled when keepalive options are set")

	idle := rawSockoptInt(t, conn, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE)
	require.Equal(t, 45, idle, "TCP_KEEPIDLE must match TCPKeepAliveIdle (seconds)")

	intvl := rawSockoptInt(t, conn, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL)
	require.Equal(t, 15, intvl, "TCP_KEEPINTVL must match TCPKeepAliveInterval (seconds)")

	probes := rawSockoptInt(t, conn, syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT)
	require.Equal(t, 5, probes, "TCP_KEEPCNT must match TCPKeepAliveProbes")

	srv := <-accepted
	require.NotNil(t, srv)

	_ = srv.Close()
}

// TestListenAcceptedConnsAreTunedLinux asserts that accepted server-
// side connections inherit the same tuning. The Listen wrapper must
// apply the per-conn knobs after Accept; without it, options on the
// exporter half are silent regardless of what the client requested.
func TestListenAcceptedConnsAreTunedLinux(t *testing.T) {
	t.Parallel()

	tr := transport.New()

	listenOpts := netopts.TransportOptions{
		SendBufferBytes:      256 * 1024,
		ReceiveBufferBytes:   256 * 1024,
		TCPKeepAliveIdle:     30 * time.Second,
		TCPKeepAliveInterval: 10 * time.Second,
		TCPKeepAliveProbes:   3,
	}

	ln, err := tr.Listen(t.Context(), "127.0.0.1:0", listenOpts)
	require.NoError(t, err)

	defer func() { _ = ln.Close() }()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	port, err := strconv.ParseUint(portStr, 10, 16)
	require.NoError(t, err)

	ep := domain.RemoteEndpoint{Host: host, Port: uint16(port)}

	accepted := make(chan net.Conn, 1)

	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			accepted <- nil

			return
		}

		accepted <- c
	}()

	dialCtx, dialCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer dialCancel()

	client, err := tr.Dial(dialCtx, ep, netopts.TransportOptions{})
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	srv := <-accepted
	require.NotNil(t, srv)

	defer func() { _ = srv.Close() }()

	rcv := rawSockoptInt(t, srv, syscall.SOL_SOCKET, syscall.SO_RCVBUF)
	snd := rawSockoptInt(t, srv, syscall.SOL_SOCKET, syscall.SO_SNDBUF)

	require.GreaterOrEqual(t, rcv, listenOpts.ReceiveBufferBytes,
		"accepted-conn SO_RCVBUF must be at least the requested size")
	require.GreaterOrEqual(t, snd, listenOpts.SendBufferBytes,
		"accepted-conn SO_SNDBUF must be at least the requested size")

	keepalive := rawSockoptInt(t, srv, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE)
	require.NotZero(t, keepalive,
		"accepted-conn must enable SO_KEEPALIVE when listen-side keepalive options are set")

	idle := rawSockoptInt(t, srv, syscall.IPPROTO_TCP, syscall.TCP_KEEPIDLE)
	require.Equal(t, 30, idle)

	intvl := rawSockoptInt(t, srv, syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL)
	require.Equal(t, 10, intvl)

	probes := rawSockoptInt(t, srv, syscall.IPPROTO_TCP, syscall.TCP_KEEPCNT)
	require.Equal(t, 3, probes)
}

// loopbackListener stands up a 127.0.0.1:<ephemeral> listener and
// returns it alongside a parsed RemoteEndpoint targeting its address.
// Cleanup is registered on t. Mirrors the shared helper in
// transport_test.go but lives here so the linux-tagged file is self-
// contained when run independently of the portable suite.
func loopbackListener(t *testing.T) (net.Listener, domain.RemoteEndpoint) {
	t.Helper()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	t.Cleanup(func() { _ = ln.Close() })

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	port, err := strconv.ParseUint(portStr, 10, 16)
	require.NoError(t, err)

	return ln, domain.RemoteEndpoint{Host: host, Port: uint16(port)}
}

// acceptOnce parks an Accept on the supplied listener and feeds the
// resulting conn (or nil on Accept error) into a buffered channel.
// Test bodies select on the channel to collect the server-side conn
// once they have completed their client-side assertions.
func acceptOnce(ln net.Listener) <-chan net.Conn {
	out := make(chan net.Conn, 1)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			out <- nil

			return
		}

		out <- c
	}()

	return out
}

// TestDialConnectTimeoutWiresDialerTimeoutLinux proves the adapter
// copies opts.DialConnectTimeout into the per-call net.Dialer.Timeout
// before issuing the connect syscall. Two cells:
//
//   - zero DialConnectTimeout dialing loopback succeeds (control:
//     proves the listener and dial path are healthy under default
//     options).
//   - 1 ns DialConnectTimeout dialing the same loopback returns a
//     timeout-class error before the connect can complete (proves
//     the field reaches net.Dialer.Timeout, since stdlib lowers a
//     non-zero Timeout into a context.DeadlineExceeded path that
//     fires before dialSerial reaches the syscall).
//
// If our impl forgot to set dialer.Timeout, the 1 ns cell would
// connect to loopback successfully and the assertion would fail.
func TestDialConnectTimeoutWiresDialerTimeoutLinux(t *testing.T) {
	t.Parallel()

	// loopbackListener registers t.Cleanup to close the listener after
	// every subtest completes. A naive defer here would race t.Parallel
	// in the subtests: the outer test returns immediately, the deferred
	// Close fires, and the parallel children dial a closed port.
	_, ep := loopbackListener(t)

	cases := []struct {
		name        string
		timeout     time.Duration
		wantTimeout bool
	}{
		{"zero timeout connects normally", 0, false},
		// 1µs is comfortably below any loopback connect latency
		// (median ~50µs on Linux) but high enough that the dialer's
		// internal timer goroutine has slack against scheduling
		// jitter under heavy CI load. Bumped from 1ns per a flake-
		// risk review.
		{"sub-microsecond fires timeout pre-syscall", 1 * time.Microsecond, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tr := transport.New()

			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()

			opts := netopts.TransportOptions{DialConnectTimeout: tc.timeout}

			conn, err := tr.Dial(ctx, ep, opts)

			if !tc.wantTimeout {
				require.NoError(t, err)
				require.NotNil(t, conn)

				_ = conn.Close()

				return
			}

			require.Error(t, err)
			require.Nil(t, conn)

			var netErr net.Error

			require.ErrorAs(t, err, &netErr,
				"timeout-class error must satisfy net.Error")
			require.True(t, netErr.Timeout(),
				"net.Error.Timeout() must report true on dialer-timeout firing")
			require.ErrorIs(t, err, context.DeadlineExceeded,
				"net.Dialer lowers a non-zero Timeout into a context deadline")
		})
	}
}

// TestDialAppliesReadDeadlineLinux proves the adapter applies
// opts.ReadDeadline so a Read against an idle peer returns
// os.ErrDeadlineExceeded. The deadline (50 ms) is short enough to
// fire well before the test guard (200 ms) but long enough not to
// race goroutine startup. The peer is held open and silent so the
// only path out of Read is the deadline.
func TestDialAppliesReadDeadlineLinux(t *testing.T) {
	t.Parallel()

	ln, ep := loopbackListener(t)

	defer func() { _ = ln.Close() }()

	accepted := acceptOnce(ln)

	tr := transport.New()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	opts := netopts.TransportOptions{ReadDeadline: 50 * time.Millisecond}

	conn, err := tr.Dial(ctx, ep, opts)
	require.NoError(t, err)

	defer func() { _ = conn.Close() }()

	srv := <-accepted
	require.NotNil(t, srv)

	defer func() { _ = srv.Close() }()

	// Semantic assertion only: the read must return an error wrapping
	// os.ErrDeadlineExceeded. Earlier revisions also asserted an
	// upper-bound elapsed time, but that turned the test into a
	// scheduler benchmark — under CI load the 50 ms deadline can
	// stretch past any reasonable wall-clock guard while the
	// deadline-exceeded error is still correct. The outer Go test
	// timeout (`go test -timeout`) bounds runaway hangs, which is
	// the only timing guarantee the test needs.
	buf := make([]byte, 1)
	n, err := conn.Read(buf)

	require.Zero(t, n)
	require.ErrorIs(t, err, os.ErrDeadlineExceeded,
		"Read must return an error wrapping os.ErrDeadlineExceeded; got %v", err)
}

// TestDialAppliesWriteDeadlineLinux proves the adapter applies
// opts.WriteDeadline. The adapter sets the deadline at dial time as
// time.Now().Add(opts.WriteDeadline); a 10 ms WriteDeadline followed
// by a 50 ms sleep makes the deadline already-expired before the
// first Write, so the Write must fail with os.ErrDeadlineExceeded.
// This avoids the brittle "fill the peer's recv buffer" pattern.
func TestDialAppliesWriteDeadlineLinux(t *testing.T) {
	t.Parallel()

	ln, ep := loopbackListener(t)

	defer func() { _ = ln.Close() }()

	accepted := acceptOnce(ln)

	tr := transport.New()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	opts := netopts.TransportOptions{WriteDeadline: 10 * time.Millisecond}

	conn, err := tr.Dial(ctx, ep, opts)
	require.NoError(t, err)

	defer func() { _ = conn.Close() }()

	srv := <-accepted
	require.NotNil(t, srv)

	defer func() { _ = srv.Close() }()

	// Sleep past the absolute write deadline so the Write below is
	// guaranteed to observe an already-expired deadline.
	time.Sleep(50 * time.Millisecond)

	n, err := conn.Write([]byte{0x01})
	require.Zero(t, n)
	require.ErrorIs(t, err, os.ErrDeadlineExceeded,
		"Write must return an error wrapping os.ErrDeadlineExceeded; got %v", err)
}
