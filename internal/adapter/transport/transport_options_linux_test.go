// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package transport_test

import (
	"context"
	"net"
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
