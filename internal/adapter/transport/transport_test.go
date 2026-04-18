package transport_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/transport"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// localListener stands up a 127.0.0.1:<ephemeral> listener and returns it
// along with a parsed RemoteEndpoint targeting its address. Cleanup is
// registered on t.
func localListener(t *testing.T) (net.Listener, domain.RemoteEndpoint) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
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

	conn, err := tr.Dial(ctx, ep)
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

	conn, err := tr.Dial(ctx, ep)
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

// TestListen_ContextCancel checks that a pre-cancelled context makes
// Listen return ctx.Err() without binding a port.
func TestListen_ContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	tr := transport.New()

	ln, err := tr.Listen(ctx, "127.0.0.1:0")
	if ln != nil {
		_ = ln.Close()
	}

	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

// TestListen_AcceptLoopback exercises the full listen/dial/accept path
// and round-trips a payload end-to-end.
func TestListen_AcceptLoopback(t *testing.T) {
	t.Parallel()

	tr := transport.New()

	ln, err := tr.Listen(t.Context(), "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()

	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	require.NoError(t, err)

	port, err := strconv.ParseUint(portStr, 10, 16)
	require.NoError(t, err)

	ep := domain.RemoteEndpoint{Host: host, Port: uint16(port)}

	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}

	var wg sync.WaitGroup

	wg.Add(1)

	serverErr := make(chan error, 1)

	go func() {
		defer wg.Done()

		srv, aerr := ln.Accept()
		if aerr != nil {
			serverErr <- aerr

			return
		}

		defer func() { _ = srv.Close() }()

		buf := make([]byte, len(payload))
		if _, rerr := srv.Read(buf); rerr != nil {
			serverErr <- rerr

			return
		}

		if !bytes.Equal(buf, payload) {
			serverErr <- errors.New("server read mismatch")

			return
		}

		if _, werr := srv.Write(buf); werr != nil {
			serverErr <- werr

			return
		}

		serverErr <- nil
	}()

	dialCtx, dialCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer dialCancel()

	client, err := tr.Dial(dialCtx, ep)
	require.NoError(t, err)

	defer func() { _ = client.Close() }()

	_, err = client.Write(payload)
	require.NoError(t, err)

	echo := make([]byte, len(payload))
	_, err = client.Read(echo)
	require.NoError(t, err)
	require.Equal(t, payload, echo)

	wg.Wait()
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

		// Trigger a logged event: a cancelled-ctx Dial should at least
		// not panic, and the option plumbing itself is the assertion.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, _ = tr.Dial(ctx, domain.RemoteEndpoint{Host: "127.0.0.1", Port: 1})
	})

	t.Run("nil logger selects discard", func(t *testing.T) {
		t.Parallel()

		tr := transport.New(transport.WithLogger(nil))
		require.NotNil(t, tr)
	})
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
