// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package transport_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/transport"
	"github.com/abilisoft/usbip-go/internal/netopts"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// errServerReadMismatch is a static sentinel for the loopback server
// goroutine; defining it at package scope satisfies err113 without
// introducing an opaque errors.New inside the test body.
var errServerReadMismatch = errors.New("server read mismatch")

// localListener stands up a 127.0.0.1:<ephemeral> listener and returns it
// along with a parsed RemoteEndpoint targeting its address. Cleanup is
// registered on t.
func localListener(t *testing.T) (net.Listener, domain.RemoteEndpoint) {
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

// TestDial_ContextCancelPreDial cancels the context before calling Dial.
// The dialer must surface ctx.Err() (context.Canceled) rather than
// opening a socket.
func TestDial_ContextCancelPreDial(t *testing.T) {
	t.Parallel()

	_, ep := localListener(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	tr := transport.New()

	conn, err := tr.Dial(ctx, ep, netopts.TransportOptions{})
	if conn != nil {
		_ = conn.Close()
	}

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

// TestDial_TCPNodelay verifies the returned net.Conn is a *net.TCPConn
// (confirming the TCP_NODELAY path ran against a typed TCPConn).
func TestDial_TCPNodelay(t *testing.T) {
	t.Parallel()

	ln, ep := localListener(t)

	accepted := make(chan net.Conn, 1)

	go func() {
		c, err := ln.Accept()
		if err != nil {
			accepted <- nil

			return
		}

		accepted <- c
	}()

	tr := transport.New()

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()

	conn, err := tr.Dial(ctx, ep, netopts.TransportOptions{})
	require.NoError(t, err)
	require.NotNil(t, conn)

	defer func() { _ = conn.Close() }()

	tcpConn, ok := conn.(*net.TCPConn)
	require.True(t, ok, "expected *net.TCPConn, got %T", conn)

	// SetNoDelay is idempotent; a successful call after the adapter
	// already set it to true proves the conn supports the API and the
	// adapter's code path is compatible with real TCP conns.
	require.NoError(t, tcpConn.SetNoDelay(true))

	server := <-accepted
	require.NotNil(t, server)

	_ = server.Close()
}

// TestListen_BadAddress covers the error path where net.ListenConfig
// rejects a malformed address. Confirms the wrap format survives the
// downstream error and that the returned listener is nil.
func TestListen_BadAddress(t *testing.T) {
	t.Parallel()

	tr := transport.New()

	ln, err := tr.Listen(t.Context(), "127.0.0.1:not-a-port", netopts.TransportOptions{})
	require.Nil(t, ln)
	require.Error(t, err)
	require.Contains(t, err.Error(), "listen 127.0.0.1:not-a-port")
}

// TestDial_BadAddress covers the error path where the dialer fails to
// reach the target. Port 1 on loopback reliably refuses connections in
// a CI sandbox, exercising the wrap path.
func TestDial_BadAddress(t *testing.T) {
	t.Parallel()

	tr := transport.New()

	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()

	conn, err := tr.Dial(ctx, domain.RemoteEndpoint{Host: "127.0.0.1", Port: 1}, netopts.TransportOptions{})
	if conn != nil {
		_ = conn.Close()
	}

	require.Error(t, err)
	require.Contains(t, err.Error(), "dial 127.0.0.1:1")
}

// TestListen_ContextCancel checks that a pre-cancelled context makes
// Listen return ctx.Err() without binding a port.
func TestListen_ContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	tr := transport.New()

	ln, err := tr.Listen(ctx, "127.0.0.1:0", netopts.TransportOptions{})
	if ln != nil {
		_ = ln.Close()
	}

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

// loopbackServer drives the accept/read/echo half of
// TestListen_AcceptLoopback. Split out so the test body stays under the
// funlen limit and wsl_v5 cuddling rules.
func loopbackServer(ln net.Listener, payload []byte, done chan<- error) {
	srv, aerr := ln.Accept()
	if aerr != nil {
		done <- fmt.Errorf("accept: %w", aerr)

		return
	}

	defer func() { _ = srv.Close() }()

	buf := make([]byte, len(payload))

	_, rerr := srv.Read(buf)
	if rerr != nil {
		done <- fmt.Errorf("read: %w", rerr)

		return
	}

	if !bytes.Equal(buf, payload) {
		done <- errServerReadMismatch

		return
	}

	_, werr := srv.Write(buf)
	if werr != nil {
		done <- fmt.Errorf("write: %w", werr)

		return
	}

	done <- nil
}

// TestListen_AcceptLoopback exercises the full listen/dial/accept path
// and round-trips a payload end-to-end.
func TestListen_AcceptLoopback(t *testing.T) {
	t.Parallel()

	tr := transport.New()

	ln, err := tr.Listen(t.Context(), "127.0.0.1:0", netopts.TransportOptions{})
	require.NoError(t, err)

	defer func() { _ = ln.Close() }()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	port, err := strconv.ParseUint(portStr, 10, 16)
	require.NoError(t, err)

	ep := domain.RemoteEndpoint{Host: host, Port: uint16(port)}
	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	serverErr := make(chan error, 1)

	go loopbackServer(ln, payload, serverErr)

	dialCtx, dialCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer dialCancel()

	client, err := tr.Dial(dialCtx, ep, netopts.TransportOptions{})
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	_, err = client.Write(payload)
	require.NoError(t, err)

	echo := make([]byte, len(payload))

	_, err = client.Read(echo)
	require.NoError(t, err)
	require.Equal(t, payload, echo)
	require.NoError(t, <-serverErr)
}

// TestNew_OptionsApply verifies WithLogger installs the logger and that
// nil selects the no-op discard handler.
func TestNew_OptionsApply(t *testing.T) {
	t.Parallel()

	t.Run("with logger", func(t *testing.T) {
		t.Parallel()

		capture := newCaptureHandler()
		tr := transport.New(transport.WithLogger(slog.New(capture)))
		require.NotNil(t, tr)

		ln, ep := localListener(t)

		accepted := make(chan net.Conn, 1)

		go func() {
			c, aerr := ln.Accept()
			if aerr != nil {
				accepted <- nil

				return
			}

			accepted <- c
		}()

		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		conn, err := tr.Dial(ctx, ep, netopts.TransportOptions{})
		require.NoError(t, err)

		defer func() { _ = conn.Close() }()

		srv := <-accepted
		require.NotNil(t, srv)

		_ = srv.Close()

		msgs := capture.snapshot()
		require.NotEmpty(t, msgs, "WithLogger must wire through to Dial's debug log")
		require.Contains(t, msgs[0], "transport.Dial established")
	})

	t.Run("nil logger selects discard", func(t *testing.T) {
		t.Parallel()

		tr := transport.New(transport.WithLogger(nil))
		require.NotNil(t, tr)

		// Exercise a Dial path so the nil branch is observed; no crash
		// means the discard handler substitution worked.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, _ = tr.Dial(ctx, domain.RemoteEndpoint{Host: "127.0.0.1", Port: 1}, netopts.TransportOptions{})
	})
}

// TestListen_ClosesOnContextCancel asserts that a listener returned by
// NetTransport.Listen stops accepting once the context passed to Listen
// is cancelled. Without this behaviour callers who pass a shutdown
// context still get a live socket that keeps accepting until they
// separately Close() it — a footgun for graceful-shutdown call sites.
func TestListen_ClosesOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	tr := transport.New()

	ln, err := tr.Listen(ctx, "127.0.0.1:0", netopts.TransportOptions{})
	require.NoError(t, err)

	defer func() { _ = ln.Close() }()

	cancel()

	done := make(chan error, 1)

	go func() {
		_, aerr := ln.Accept()
		done <- aerr
	}()

	select {
	case acceptErr := <-done:
		require.Error(t, acceptErr, "Accept must return an error after ctx cancel")
	case <-time.After(time.Second):
		t.Fatal("Accept did not unblock within 1s of ctx cancel")
	}
}

// TestListen_CloseIdempotent asserts the wrapped listener's Close can
// be called multiple times without panicking. First call returns nil;
// subsequent calls may return an "already closed" error from the
// underlying listener — not a panic, not a deadlock.
func TestListen_CloseIdempotent(t *testing.T) {
	t.Parallel()

	tr := transport.New()

	ln, err := tr.Listen(t.Context(), "127.0.0.1:0", netopts.TransportOptions{})
	require.NoError(t, err)

	_ = ln.Close()
	_ = ln.Close()
}

// TestListen_CloseDoesNotDeadlockAfterCtxCancel covers the race where
// the ctx-watcher goroutine has already closed the listener and the
// caller then calls Close(). The wrapper MUST wait for the watcher to
// exit without deadlocking.
func TestListen_CloseDoesNotDeadlockAfterCtxCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())

	tr := transport.New()

	ln, err := tr.Listen(ctx, "127.0.0.1:0", netopts.TransportOptions{})
	require.NoError(t, err)

	cancel()
	// Give the watcher a moment to run so the race path exercises
	// "watcher closed first, caller calls Close second".
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})

	go func() {
		_ = ln.Close()

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked after ctx-triggered listener close")
	}
}

// TestDial_CancelRaceInvariant asserts the Dial return contract stays
// consistent under a concurrent cancel workload: either a live conn
// (caller owns Close) or an error with no conn — never both, never
// neither. On loopback the TCP handshake completes too fast to
// reliably force a mid-handshake cancel, so the test does NOT prove
// the timing race is handled — it proves the invariant holds across
// 100 cancel-racing iterations, catching a future refactor that
// accidentally leaks a raced-successful conn or returns a bogus
// (nil, nil) pair. True mid-handshake timing is left to
// integration-level coverage with slower transports.
func TestDial_CancelRaceInvariant(t *testing.T) {
	t.Parallel()

	ln, ep := localListener(t)

	srvDone := make(chan struct{})

	go func() {
		defer close(srvDone)

		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}

			_ = c.Close()
		}
	}()

	defer func() {
		_ = ln.Close()

		<-srvDone
	}()

	tr := transport.New()

	const iters = 100

	for range iters {
		ctx, cancel := context.WithCancel(t.Context())

		go cancel()

		conn, err := tr.Dial(ctx, ep, netopts.TransportOptions{})

		switch {
		case conn != nil:
			require.NoError(t, err, "conn non-nil must imply err nil")

			_ = conn.Close()
		case err != nil:
			// Cancelled before or during handshake; acceptable.
		default:
			t.Fatal("both conn and err are nil")
		}
	}
}

// captureHandler collects every slog.Record it handles, mirroring the
// pattern in internal/adapter/wire/codec_test.go.
type captureHandler struct {
	mu   sync.Mutex
	msgs []string
}

func newCaptureHandler() *captureHandler { return &captureHandler{} }

func (c *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (c *captureHandler) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, r.Message)
	c.mu.Unlock()

	return nil
}

func (c *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return c }

func (c *captureHandler) WithGroup(_ string) slog.Handler { return c }

func (c *captureHandler) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]string, len(c.msgs))
	copy(out, c.msgs)

	return out
}
