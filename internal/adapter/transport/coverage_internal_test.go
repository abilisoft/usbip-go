// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package transport

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"testing"

	"github.com/abilisoft/usbip-go/internal/netopts"
	"github.com/stretchr/testify/require"
)

// errInjectedNonFatal is a static sentinel returned by sockopt-test
// fn closures that should fall through to the warn-and-swallow
// branch of applySockopt.
var errInjectedNonFatal = errors.New("injected non-fatal sockopt error")

// TestApplySockoptFatalErrorReturnsWrapped covers applySockopt's
// fatal-classification branch by passing a closure whose returned
// error is one of the isSockoptFatal sentinels (net.ErrClosed,
// ENOTCONN, EBADF). The function MUST surface a wrapped error so
// the caller can close the conn and return.
func TestApplySockoptFatalErrorReturnsWrapped(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	cases := []struct {
		name string
		err  error
	}{
		{"net.ErrClosed", net.ErrClosed},
		{"syscall.ENOTCONN", syscall.ENOTCONN},
		{"syscall.EBADF", syscall.EBADF},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := applySockopt(t.Context(), "dial", "SO_SNDBUF", logger,
				func() error { return tc.err })
			require.Error(t, err)
			require.ErrorIs(t, err, tc.err,
				"fatal sockopt failure must wrap the underlying errno")
			require.Contains(t, err.Error(), "dial")
			require.Contains(t, err.Error(), "SO_SNDBUF")
		})
	}
}

// TestApplySockoptNonFatalErrorIsSwallowed covers applySockopt's
// non-fatal branch: an injected error that is NOT one of the fatal
// sentinels must be logged at warn and the function must return nil
// so the session continues with whatever tuning landed.
func TestApplySockoptNonFatalErrorIsSwallowed(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

	err := applySockopt(t.Context(), "accept", "SO_RCVBUF", logger,
		func() error { return errInjectedNonFatal })
	require.NoError(t, err,
		"non-fatal sockopt failures must be logged + swallowed")
}

// TestApplySockoptNoErrorReturnsNil covers the success path: fn
// returns nil, applySockopt returns nil without invoking the
// classifier.
func TestApplySockoptNoErrorReturnsNil(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)
	called := false

	err := applySockopt(t.Context(), "dial", "SO_SNDBUF", logger,
		func() error {
			called = true
			return nil
		})
	require.NoError(t, err)
	require.True(t, called)
}

// fakeListener is a minimal net.Listener stand-in that hands the
// caller a pre-staged result (conn or error) on each Accept. It
// supports the tuningListener wrap in two flavours used by the
// tests below: returning an injected accept error, and returning a
// non-TCP conn.
type fakeListener struct {
	mu       sync.Mutex
	accepts  []fakeAccept
	closed   bool
	closeErr error
}

type fakeAccept struct {
	conn net.Conn
	err  error
}

func (l *fakeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.accepts) == 0 {
		return nil, io.EOF
	}

	next := l.accepts[0]

	l.accepts = l.accepts[1:]

	return next.conn, next.err
}

func (l *fakeListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}

	l.closed = true

	return l.closeErr
}

func (l *fakeListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

// TestTuningListenerAcceptErrorIsWrapped covers tuningListener.Accept's
// error path: when the underlying listener returns an error, the
// wrap surfaces it with the "accept:" prefix.
func TestTuningListenerAcceptErrorIsWrapped(t *testing.T) {
	t.Parallel()

	inner := &fakeListener{accepts: []fakeAccept{{err: io.ErrUnexpectedEOF}}}
	tl := &tuningListener{
		Listener: inner,
		opts:     netopts.TransportOptions{SendBufferBytes: 1024},
		logger:   slog.New(slog.DiscardHandler),
	}

	conn, err := tl.Accept()
	require.Error(t, err)
	require.Nil(t, conn)
	require.Contains(t, err.Error(), "accept")
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

// TestTuningListenerNonTCPConnPassesThrough covers the non-TCP
// branch: when Accept returns a conn that is not *net.TCPConn (e.g.
// a net.Pipe end), tuningListener returns it untouched without
// attempting to apply socket options.
func TestTuningListenerNonTCPConnPassesThrough(t *testing.T) {
	t.Parallel()

	clientPipe, serverPipe := net.Pipe()

	defer func() { _ = clientPipe.Close() }()
	defer func() { _ = serverPipe.Close() }()

	inner := &fakeListener{accepts: []fakeAccept{{conn: serverPipe}}}
	tl := &tuningListener{
		Listener: inner,
		opts:     netopts.TransportOptions{SendBufferBytes: 1024},
		logger:   slog.New(slog.DiscardHandler),
	}

	got, err := tl.Accept()
	require.NoError(t, err)
	require.Same(t, serverPipe, got,
		"non-TCP conns must be returned untouched without sockopt tuning")
}

// TestTuningListenerFatalTuneClosesConn covers the fatal-tune
// branch: a TCP conn that is already closed forces SetWriteBuffer
// to fail with EBADF (fatal), tuningListener.Accept must close the
// conn and surface the wrapped error.
func TestTuningListenerFatalTuneClosesConn(t *testing.T) {
	t.Parallel()

	// Open a real loopback TCP pair so we have a *net.TCPConn, then
	// close it before handing it to Accept. Subsequent SetWriteBuffer
	// returns EBADF (fatal classification).
	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { _ = ln.Close() }()

	dialerCtx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var d net.Dialer

	dialed, err := d.DialContext(dialerCtx, "tcp", ln.Addr().String())
	require.NoError(t, err)

	tcpConn, ok := dialed.(*net.TCPConn)
	require.True(t, ok)

	require.NoError(t, tcpConn.Close())

	inner := &fakeListener{accepts: []fakeAccept{{conn: tcpConn}}}
	tl := &tuningListener{
		Listener: inner,
		opts:     netopts.TransportOptions{SendBufferBytes: 1024},
		logger:   slog.New(slog.DiscardHandler),
	}

	got, accErr := tl.Accept()
	require.Error(t, accErr)
	require.Nil(t, got)
	require.Contains(t, accErr.Error(), "accept")
}

// TestTuneDeadlinesFatalReadDeadline covers tuneDeadlines's fatal
// branch: a closed TCP conn returns net.ErrClosed on
// SetReadDeadline; the helper surfaces the wrapped error.
func TestTuneDeadlinesFatalReadDeadline(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { _ = ln.Close() }()

	var d net.Dialer

	dialed, err := d.DialContext(t.Context(), "tcp", ln.Addr().String())
	require.NoError(t, err)

	tcpConn, ok := dialed.(*net.TCPConn)
	require.True(t, ok)

	require.NoError(t, tcpConn.Close())

	tuneErr := tuneDeadlines(
		t.Context(), tcpConn,
		netopts.TransportOptions{ReadDeadline: 1},
		"dial", slog.New(slog.DiscardHandler),
	)
	require.Error(t, tuneErr)
}
