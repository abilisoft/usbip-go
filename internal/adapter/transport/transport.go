package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"syscall"

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

	// TCP_NODELAY is a performance knob (§6.3) — turning off Nagle so
	// the short USB/IP handshake frames ship immediately. If the kernel
	// rejects the sockopt (exotic stacks, LD_PRELOAD wrappers) the TCP
	// handshake itself still succeeded and the connection is usable,
	// just with Nagle's coalescing. Dropping a valid conn on a perf
	// knob failure would convert a recoverable warning into an
	// availability outage; log at warn instead and keep the conn.
	//
	// Dead-conn classes (net.ErrClosed, ENOTCONN, EBADF) are the
	// exception: the sockopt only fails with those when the conn is
	// already broken, so returning it to the caller postpones the
	// failure to the next Write with a wrapping that obscures the
	// true cause. Surface the errno and close the conn.
	nerr := tcpConn.SetNoDelay(true)
	if nerr != nil {
		if isSockoptFatal(nerr) {
			_ = conn.Close()

			return nil, fmt.Errorf("dial %s: set TCP_NODELAY: %w", addr, nerr)
		}

		t.logger.LogAttrs(ctx, slog.LevelWarn, "transport.Dial TCP_NODELAY rejected",
			slog.String("remote", addr), slog.Any("err", nerr))
	}

	t.logger.LogAttrs(ctx, slog.LevelDebug, "transport.Dial established",
		slog.String("remote", addr), slog.Bool("tcp", true), slog.Bool("nodelay", nerr == nil))

	return conn, nil
}

// isSockoptFatal reports whether a sockopt-setting error classifies
// as "conn is already dead" rather than "sockopt rejected on a live
// conn". A fatal errno (net.ErrClosed, ENOTCONN, EBADF) means the
// kernel already considers the socket unusable; returning it up the
// call chain would only delay the real failure.
func isSockoptFatal(err error) bool {
	return errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.ENOTCONN) ||
		errors.Is(err, syscall.EBADF)
}

// Listen binds addr and returns a ctx-bound net.Listener. The listener
// is closed automatically when ctx is cancelled, so graceful-shutdown
// call sites in §7 can drive a daemon teardown by cancelling one root
// context without having to track the listener separately. The
// returned Listener's own Close is idempotent and waits for the
// watcher goroutine to exit, so callers cannot leak it.
func (t *NetTransport) Listen(ctx context.Context, addr string) (net.Listener, error) {
	ctxErr := ctx.Err()
	if ctxErr != nil {
		return nil, fmt.Errorf("listen %s: context cancelled before bind: %w", addr, ctxErr)
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: bind failed: %w", addr, err)
	}

	t.logger.LogAttrs(ctx, slog.LevelDebug, "transport.Listen bound",
		slog.String("addr", ln.Addr().String()))

	return newCtxListener(ctx, ln), nil
}

// ctxListener wraps a net.Listener so that its lifetime is bound to a
// context. A goroutine waits on either ctx.Done or an explicit Close
// and invokes the underlying listener's Close exactly once via
// closeOnce. Close itself is idempotent and blocks until the watcher
// has observed the stop signal, so callers who Close() immediately
// after constructing the wrapper cannot race the goroutine into a
// leak.
//
// Lifecycle obligation: callers MUST either cancel the context passed
// to Listen OR call the returned listener's Close. Dropping the
// listener reference without doing one of those strands the watcher
// goroutine until the context is cancelled — the same FD-leak
// contract stdlib net.Listener already imposes, with a bundled
// goroutine on top. Standard defer-Close or shutdown-context patterns
// satisfy this; panic-without-recover paths do not.
type ctxListener struct {
	net.Listener

	stop      chan struct{}
	stopOnce  sync.Once
	closeOnce sync.Once
	closeErr  error
	watcher   chan struct{}
}

func newCtxListener(ctx context.Context, ln net.Listener) *ctxListener {
	cl := &ctxListener{
		Listener: ln,
		stop:     make(chan struct{}),
		watcher:  make(chan struct{}),
	}

	go cl.watch(ctx)

	return cl
}

// Close signals the watcher to stop, closes the underlying listener at
// most once, and blocks until the watcher has exited. The order is:
// (1) close the stop chan so the watcher observes the exit signal;
// (2) close the listener exactly once (watcher may have already done
// so); (3) wait for the watcher goroutine to exit.
func (cl *ctxListener) Close() error {
	cl.stopOnce.Do(func() { close(cl.stop) })
	cl.closeOnce.Do(func() { cl.closeErr = cl.Listener.Close() })
	<-cl.watcher

	return cl.closeErr
}

// watch closes the underlying listener on whichever signal arrives
// first — ctx cancellation or an explicit Close via the stop channel.
// closeOnce guards against double-close if both fire close together.
func (cl *ctxListener) watch(ctx context.Context) {
	defer close(cl.watcher)

	select {
	case <-ctx.Done():
		cl.closeOnce.Do(func() { cl.closeErr = cl.Listener.Close() })
	case <-cl.stop:
	}
}

// noopLogger returns a *slog.Logger that discards all records. Using a
// real discard handler keeps the hot path allocation-free compared to
// nil-checks before every log call.
func noopLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
