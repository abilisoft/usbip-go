package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// NetTransport is the pure-Go implementation of the app.Transport
// contract declared in spec §5.1. It wraps net.Dialer / net.ListenConfig
// so that every dial and listen observes a caller-supplied context, and
// flips TCP_NODELAY on dialed connections to minimise USB/IP handshake
// latency.
//
// Zero-value NetTransport{} is NOT valid — use New to obtain one with
// the default dialer and a no-op logger installed. NetTransport is
// safe for concurrent use; all mutating configuration is applied at
// construction time via Option values.
type NetTransport struct {
	dialer *net.Dialer
	logger *slog.Logger
}

// Option configures a NetTransport at construction time.
type Option func(*NetTransport)

// WithLogger installs l as the NetTransport's logger. Passing nil
// selects a discarding handler so call sites never have to nil-check.
// This mirrors the Codec option pattern in internal/adapter/wire so
// both adapters share a single logging convention (spec §3.6).
func WithLogger(l *slog.Logger) Option {
	return func(t *NetTransport) {
		if l == nil {
			t.logger = noopLogger()

			return
		}

		t.logger = l
	}
}

// New constructs a NetTransport ready to dial or listen. A fresh
// net.Dialer is allocated per instance so option-driven future knobs
// (timeout, keepalive) don't leak between callers.
func New(opts ...Option) *NetTransport {
	t := &NetTransport{
		dialer: &net.Dialer{},
		logger: noopLogger(),
	}
	for _, opt := range opts {
		opt(t)
	}

	return t
}

// Dial connects to r over TCP using ctx for cancellation. Port 0 in r
// is normalised to domain.DefaultPort per spec §3 — the net.Dialer
// would otherwise try to connect to port 0 which is a kernel-reserved
// sentinel. TCP_NODELAY is set on the returned connection so the
// USB/IP handshake's small frames are not Nagle-delayed.
func (t *NetTransport) Dial(ctx context.Context, r domain.RemoteEndpoint) (net.Conn, error) {
	addr := r.NormalizePort().String()

	conn, err := t.dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}

	// Typed TCP conns carry SetNoDelay; the empty-network fallback path
	// in net (e.g. a mock conn) would not, but DialContext("tcp", ...)
	// is guaranteed by the stdlib to return a *net.TCPConn.
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		t.logger.LogAttrs(ctx, slog.LevelDebug, "transport.Dial established",
			slog.String("remote", addr), slog.Bool("tcp", false))

		return conn, nil
	}

	nerr := tcpConn.SetNoDelay(true)
	if nerr != nil {
		_ = conn.Close()

		return nil, fmt.Errorf("set nodelay %s: %w", addr, nerr)
	}

	t.logger.LogAttrs(ctx, slog.LevelDebug, "transport.Dial established",
		slog.String("remote", addr), slog.Bool("tcp", true))

	return conn, nil
}

// Listen binds addr and returns the underlying net.Listener verbatim.
// net.ListenConfig forwards ctx to the resolver but not to the bind
// itself, so a pre-cancelled ctx against a literal IP would otherwise
// succeed — we check ctx up front so graceful-shutdown call sites in
// §7 see the cancel signal they expect.
func (t *NetTransport) Listen(ctx context.Context, addr string) (net.Listener, error) {
	ctxErr := ctx.Err()
	if ctxErr != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, ctxErr)
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	t.logger.LogAttrs(ctx, slog.LevelDebug, "transport.Listen bound",
		slog.String("addr", ln.Addr().String()))

	return ln, nil
}

// noopLogger returns a *slog.Logger that discards all records. Using a
// real discard handler keeps the hot path allocation-free compared to
// nil-checks before every log call.
func noopLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
