// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/abilisoft/usbip-go/internal/netopts"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// NetTransport is the pure-Go implementation of the app.Transport
// surface declared in v1 contract §5.1. It wraps net.Dialer / net.ListenConfig
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
// both adapters share a single logging convention (v1 contract §3.6).
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
// is normalised to domain.DefaultPort per v1 contract §3 — the net.Dialer
// would otherwise try to connect to port 0 which is a kernel-reserved
// sentinel. TCP_NODELAY is set on the returned connection so the
// USB/IP handshake's small frames are not Nagle-delayed.
//
// Non-zero opts fields are honored on the dialed conn:
//
//   - DialConnectTimeout caps the connect phase via a per-call
//     net.Dialer copy (the embedded dialer is left untouched so
//     concurrent Dials do not race on the timeout field).
//   - SendBufferBytes / ReceiveBufferBytes call SetWriteBuffer /
//     SetReadBuffer; Linux doubles the requested value internally.
//   - TCPKeepAlive{Idle,Interval,Probes} call SetKeepAliveConfig
//     (Go ≥ 1.23) and enable SO_KEEPALIVE.
//   - ReadDeadline / WriteDeadline call SetReadDeadline /
//     SetWriteDeadline; the deadlines are absolute timestamps measured
//     from the moment of the Dial call.
//
// Buffer / keepalive / deadline failures are logged at warn unless the
// conn is already dead (see isSockoptFatal); a perf knob falling over
// must not turn a usable conn into an availability outage.
func (t *NetTransport) Dial(
	ctx context.Context,
	r domain.RemoteEndpoint,
	opts netopts.TransportOptions,
) (net.Conn, error) {
	addr := r.NormalizePort().String()

	// A per-call dialer copy keeps DialConnectTimeout (when set) from
	// mutating the shared embedded dialer; concurrent Dials must not
	// race on the Timeout field.
	dialer := *t.dialer
	if opts.DialConnectTimeout > 0 {
		dialer.Timeout = opts.DialConnectTimeout
	}

	conn, err := dialer.DialContext(ctx, "tcp", addr)
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
		if isSockoptFatal(nerr) {
			_ = conn.Close()

			return nil, fmt.Errorf("dial %s: set TCP_NODELAY: %w", addr, nerr)
		}

		t.logger.LogAttrs(ctx, slog.LevelWarn, "transport.Dial TCP_NODELAY rejected",
			slog.String("remote", addr), slog.Any("err", nerr))
	}

	tuneErr := tuneTCPConn(ctx, tcpConn, opts, "dial", t.logger)
	if tuneErr != nil {
		_ = conn.Close()

		return nil, fmt.Errorf("dial %s: %w", addr, tuneErr)
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

// tuneTCPConn applies the non-zero fields on opts to conn. role is
// "dial" or "accept" and surfaces in log attributes so an operator
// reading journalctl can tell which side of a session failed a
// sockopt. Fatal errnos (conn already dead) bubble up so the caller
// can close + return; non-fatal failures are logged at warn so the
// session continues with whatever subset of tuning succeeded.
func tuneTCPConn(
	ctx context.Context,
	conn *net.TCPConn,
	opts netopts.TransportOptions,
	role string,
	logger *slog.Logger,
) error {
	if opts.SendBufferBytes > 0 {
		applyErr := applySockopt(ctx, role, "SO_SNDBUF", logger,
			func() error { return conn.SetWriteBuffer(opts.SendBufferBytes) })
		if applyErr != nil {
			return applyErr
		}
	}

	if opts.ReceiveBufferBytes > 0 {
		applyErr := applySockopt(ctx, role, "SO_RCVBUF", logger,
			func() error { return conn.SetReadBuffer(opts.ReceiveBufferBytes) })
		if applyErr != nil {
			return applyErr
		}
	}

	keepAliveErr := tuneKeepAlive(ctx, conn, opts, role, logger)
	if keepAliveErr != nil {
		return keepAliveErr
	}

	return tuneDeadlines(ctx, conn, opts, role, logger)
}

// applySockopt invokes fn (a single sockopt setter) and classifies
// any returned error: fatal errnos surface as a wrapped error the
// caller propagates, non-fatal failures are logged at warn and
// swallowed so the session continues with whatever tuning landed.
// Centralising the policy keeps tuneTCPConn under the project
// cognitive-complexity cap.
func applySockopt(
	ctx context.Context,
	role, name string,
	logger *slog.Logger,
	fn func() error,
) error {
	err := fn()
	if err == nil {
		return nil
	}

	if isSockoptFatal(err) {
		return fmt.Errorf("%s: %s: %w", role, name, err)
	}

	logger.LogAttrs(ctx, slog.LevelWarn, "transport tuneTCPConn rejected",
		slog.String("role", role), slog.String("opt", name), slog.Any("err", err))

	return nil
}

// tuneKeepAlive configures SO_KEEPALIVE + TCP_KEEPIDLE/INTVL/CNT via
// SetKeepAliveConfig (Go ≥ 1.23). Each non-zero field is forwarded;
// zero fields leave the OS default in place. Split from tuneTCPConn
// so the parent stays under the project cyclomatic-complexity cap.
func tuneKeepAlive(
	ctx context.Context,
	conn *net.TCPConn,
	opts netopts.TransportOptions,
	role string,
	logger *slog.Logger,
) error {
	if opts.TCPKeepAliveIdle == 0 &&
		opts.TCPKeepAliveInterval == 0 &&
		opts.TCPKeepAliveProbes == 0 {
		return nil
	}

	cfg := net.KeepAliveConfig{
		Enable:   true,
		Idle:     opts.TCPKeepAliveIdle,
		Interval: opts.TCPKeepAliveInterval,
		Count:    opts.TCPKeepAliveProbes,
	}

	return applySockopt(ctx, role, "SetKeepAliveConfig", logger,
		func() error { return conn.SetKeepAliveConfig(cfg) })
}

// tuneDeadlines applies the static read/write deadlines (when set) to
// the conn. Deadlines are absolute timestamps; a future
// SetReadDeadline / SetWriteDeadline by the caller can clear them
// (zero time) once the userspace handshake completes.
func tuneDeadlines(
	ctx context.Context,
	conn *net.TCPConn,
	opts netopts.TransportOptions,
	role string,
	logger *slog.Logger,
) error {
	now := time.Now()

	if opts.ReadDeadline > 0 {
		applyErr := applySockopt(ctx, role, "SetReadDeadline", logger,
			func() error { return conn.SetReadDeadline(now.Add(opts.ReadDeadline)) })
		if applyErr != nil {
			return applyErr
		}
	}

	if opts.WriteDeadline > 0 {
		return applySockopt(ctx, role, "SetWriteDeadline", logger,
			func() error { return conn.SetWriteDeadline(now.Add(opts.WriteDeadline)) })
	}

	return nil
}

// Listen binds addr and returns a ctx-bound net.Listener. The listener
// is closed automatically when ctx is cancelled, so graceful-shutdown
// call sites in §7 can drive a daemon teardown by cancelling one root
// context without having to track the listener separately. The
// returned Listener's own Close is idempotent and waits for the
// watcher goroutine to exit, so callers cannot leak it.
//
// When opts carries non-zero tuning fields, accepted server-side
// connections are tuned by tuneTCPConn before they are returned from
// Accept. A failure to apply a tuning knob does NOT propagate as an
// Accept error: the conn is returned to the caller with a logged
// warning, mirroring Dial's "perf knob failure must not become an
// availability outage" policy. Fatal errnos (conn already dead) close
// the conn and surface as Accept errors.
func (t *NetTransport) Listen(
	ctx context.Context,
	addr string,
	opts netopts.TransportOptions,
) (net.Listener, error) {
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

	wrapped := newCtxListener(ctx, ln)
	if opts == (netopts.TransportOptions{}) {
		return wrapped, nil
	}

	return &tuningListener{Listener: wrapped, opts: opts, logger: t.logger}, nil
}

// tuningListener wraps a ctxListener so every accepted conn is tuned
// per opts before it is returned to the caller. Accept-time tuning is
// the only place we can reach the server-side TCPConn; a daemon path
// that consumes accepted conns directly (without Listen owning the
// accept) must apply tuning itself.
type tuningListener struct {
	net.Listener

	opts   netopts.TransportOptions
	logger *slog.Logger
}

// Accept blocks on the underlying Listener and tunes each returned
// TCP conn before handing it to the caller. Non-TCP conns (test
// fakes) bypass the tuning path. A fatal sockopt error closes the
// conn and surfaces as an Accept error so the caller cannot use a
// half-broken connection; non-fatal failures are logged and the conn
// is returned with whatever subset of tuning succeeded.
func (l *tuningListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}

	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return conn, nil
	}

	tuneErr := tuneTCPConn(context.Background(), tcpConn, l.opts, "accept", l.logger)
	if tuneErr != nil {
		_ = conn.Close()

		return nil, fmt.Errorf("accept: %w", tuneErr)
	}

	return conn, nil
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
