package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// Exporter is the use-case service that exports local USB devices via
// the usbip_host kernel surface. One Exporter is sufficient for a whole
// daemon process; construct via NewExporter and release via Shutdown.
// The zero value is not usable — NewExporter initialises required state.
//
// Every handle and counter is mutex-guarded so the accept loop, the
// per-session handler goroutines, Sessions(), and Shutdown() can all
// interleave safely under the race detector.
type Exporter struct {
	kernel    ExporterKernel
	events    KernelEvents
	transport Transport
	codec     ProtocolCodec
	clock     Clock
	logger    *slog.Logger
	metrics   *Metrics

	cfg             exporterLimits
	acceptLim       acceptLimiter
	acl             *aclChecker
	shutdownTimeout time.Duration

	mu          sync.RWMutex
	shutdown    bool
	serving     bool
	listener    net.Listener
	sessions    map[domain.SessionID]*sessionHandle
	perPeer     map[string]int
	subscribers []*sessionEventSubscriber

	// wg tracks the ctx-listener-closer goroutine spawned by Serve;
	// Serve waits on it before returning.
	wg sync.WaitGroup

	// sessionsWG tracks per-connection handler goroutines and their
	// handshake-timeout watchers. Serve deliberately does NOT wait on
	// sessionsWG (spec §3.4: Serve returns on ctx cancel; Shutdown
	// drains in-flight sessions bounded by its own ctx).
	sessionsWG sync.WaitGroup

	// sessionsDrainedOnce lazily spawns a single goroutine that waits
	// on sessionsWG and closes sessionsDrained. Multiple Shutdown
	// calls share this drain future so a truly-stuck handler cannot
	// leak one waiter goroutine per Shutdown. The once-init happens
	// inside waitSessionsBounded — at construction time Serve may not
	// be up yet, so sessionsWG has no participants to wait for.
	sessionsDrainedOnce sync.Once
	sessionsDrained     chan struct{}
}

// sessionHandle is the per-session bookkeeping entry. done is closed
// exactly once when the session handler returns (graceful or error).
// Shutdown and WatchSessions (Task 5.12) observe done to synchronise
// with session termination. conn is the accepted net.Conn; Shutdown
// force-closes it to unwedge a handler parked in kernel.ExportOnConn
// when the drain deadline expires.
type sessionHandle struct {
	session   domain.Session
	done      chan struct{}
	closeOnce sync.Once
	peerKey   string
	conn      net.Conn
}

// cancel closes done exactly once. Safe to call from any goroutine.
func (h *sessionHandle) cancel() {
	h.closeOnce.Do(func() { close(h.done) })
}

// NewExporter constructs an Exporter from functional options. Required
// dependencies missing from opts cause a panic because a missing
// dependency is a programming error, not a runtime condition worth
// propagating up the call stack. Option-driven validation failures
// (e.g. malformed ACL CIDR strings) also panic here; use
// NewExporterWithError when the caller needs to surface such errors.
func NewExporter(opts ...ExporterOption) *Exporter {
	exp, err := NewExporterWithError(opts...)
	if err != nil {
		panic(err)
	}

	return exp
}

// NewExporterWithError is the fallible constructor variant. It returns
// the same Exporter NewExporter would, plus any option-validation
// error (today only ErrACLInvalid). Missing-dependency errors still
// panic — those are programming bugs, not runtime conditions.
func NewExporterWithError(opts ...ExporterOption) (*Exporter, error) {
	cfg := exporterConfig{clock: RealClock{}, logger: slog.Default()}

	for _, opt := range opts {
		opt(&cfg)
	}

	requireExporterDeps(&cfg)

	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}

	acl, err := parseACL(cfg.aclCIDRs)
	if err != nil {
		return nil, err
	}

	if cfg.metrics == nil {
		cfg.metrics = MustNewMetrics(nil)
	}

	if !cfg.buildInfo.empty() {
		cfg.metrics.SetBuildInfo(
			cfg.buildInfo.version, cfg.buildInfo.commit, cfg.buildInfo.goVersion)
	}

	return &Exporter{
		kernel:          cfg.kernel,
		events:          cfg.events,
		transport:       cfg.transport,
		codec:           cfg.codec,
		clock:           cfg.clock,
		logger:          cfg.logger,
		metrics:         cfg.metrics,
		cfg:             resolveExporterLimits(&cfg),
		acceptLim:       newAcceptLimiter(resolveAcceptRate(&cfg), resolveAcceptBurst(&cfg)),
		acl:             acl,
		shutdownTimeout: cfg.shutdownTimeout,
		sessions:        make(map[domain.SessionID]*sessionHandle),
		perPeer:         make(map[string]int),
		sessionsDrained: make(chan struct{}),
	}, nil
}

// requireExporterDeps panics when any of the mandatory option-supplied
// dependencies is nil. Split from NewExporter to keep cyclomatic
// complexity within the project cap.
func requireExporterDeps(cfg *exporterConfig) {
	if cfg.kernel == nil {
		panic("app.NewExporter: ExporterKernel is required (use WithExporterKernel)")
	}

	if cfg.events == nil {
		panic("app.NewExporter: KernelEvents is required (use WithExporterEvents)")
	}

	if cfg.transport == nil {
		panic("app.NewExporter: Transport is required (use WithExporterTransport)")
	}

	if cfg.codec == nil {
		panic("app.NewExporter: ProtocolCodec is required (use WithExporterCodec)")
	}
}

// Bind delegates to kernel.Bind per spec §5.3. Errors are wrapped with
// the busid so callers that bind many devices can identify which failed.
func (e *Exporter) Bind(ctx context.Context, busID domain.BusID) error {
	err := e.kernel.Bind(ctx, busID)
	if err != nil {
		e.metrics.ExporterBind(classifyBindError(err))

		return fmt.Errorf("bind %s: %w", busID, err)
	}

	e.metrics.ExporterBind(BindOutcomeOK)

	return nil
}

// Unbind delegates to kernel.Unbind per spec §5.3. Errors are wrapped
// with the busid.
func (e *Exporter) Unbind(ctx context.Context, busID domain.BusID) error {
	err := e.kernel.Unbind(ctx, busID)
	if err != nil {
		e.metrics.ExporterUnbind(classifyUnbindError(err))

		return fmt.Errorf("unbind %s: %w", busID, err)
	}

	e.metrics.ExporterUnbind(UnbindOutcomeOK)

	return nil
}

// classifyBindError maps a kernel Bind error onto the §11.5.5
// bind_total outcome label. Only the well-known domain sentinels get
// a specific label; anything else falls through to "error" so the
// catalog never grows an unbounded outcome string.
func classifyBindError(err error) BindOutcome {
	switch {
	case errors.Is(err, domain.ErrDeviceAlreadyBound):
		return BindOutcomeAlreadyBound
	case errors.Is(err, domain.ErrDeviceNotFound):
		return BindOutcomeNotFound
	case errors.Is(err, domain.ErrPermission):
		return BindOutcomePermission
	}

	return BindOutcomeError
}

// classifyUnbindError mirrors classifyBindError for the unbind path.
func classifyUnbindError(err error) UnbindOutcome {
	switch {
	case errors.Is(err, domain.ErrDeviceNotBound):
		return UnbindOutcomeNotBound
	case errors.Is(err, domain.ErrPermission):
		return UnbindOutcomePermission
	}

	return UnbindOutcomeError
}

// ListAvailable forwards to kernel.ListLocalDevices per spec §5.3. The
// kernel adapter is the authoritative source; the Exporter does not
// maintain a cache of its own.
func (e *Exporter) ListAvailable(ctx context.Context) ([]domain.Device, error) {
	devs, err := e.kernel.ListLocalDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list local devices: %w", err)
	}

	return devs, nil
}

// Serve runs the accept loop per spec §5.3. Each accepted connection
// is dispatched to a per-connection goroutine via handleConn; the
// accept loop returns when ctx is cancelled, the listener returns a
// permanent error, or Shutdown is called. Returns ErrAlreadyShutdown
// if invoked after Shutdown. Serve is NOT safe to call concurrently
// from multiple goroutines on the same Exporter — overlapping accept
// loops would fight over shared bookkeeping.
func (e *Exporter) Serve(ctx context.Context, listener net.Listener) error {
	err := e.startServing(listener)
	if err != nil {
		return err
	}

	defer e.stopServing()

	stopWatcher := e.spawnCtxListenerCloser(ctx, listener)

	loopErr := e.acceptLoop(ctx, listener)

	// Release the ctx-listener-closer before waiting on wg. When
	// acceptLoop exits via Shutdown-driven listener close the ctx is
	// still live and the watcher is parked on ctx.Done; without this
	// explicit stop, wg.Wait would block forever. When acceptLoop
	// exits via ctx cancel the watcher has already returned via its
	// ctx.Done branch, so this call is a no-op on the stop channel's
	// close-once guard.
	stopWatcher()

	// Wait for the ctx-listener-closer only. Session handlers run on
	// sessionsWG; Shutdown drains them with a bounded deadline per
	// spec §3.4. If Serve also waited on sessionsWG here, an in-flight
	// ExportOnConn would block Serve's return past ctx cancel.
	e.wg.Wait()

	return loopErr
}

// Shutdown stops accepting new connections and drains in-flight
// session handlers. Per spec §3.4: new accepts are refused, existing
// handlers are signalled to exit, and the call waits for them bounded
// by the provided ctx deadline. Returns nil when the drain completes
// before the deadline; a ctx.Err-wrapped error when it does not.
// Idempotent: a second Shutdown returns nil after another drain.
//
// When the caller's ctx carries no deadline and the Exporter was built
// with WithExporterShutdownTimeout(d>0), the option's value is applied
// as an internal backstop deadline so a wedged ExportOnConn cannot
// block Shutdown forever despite a configured timeout.
func (e *Exporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()

	if !e.shutdown {
		e.shutdown = true
	}

	// Capture and clear the tracked listener under the same lock so
	// Shutdown closes it exactly once even if called concurrently. The
	// listener-close drives acceptLoop to return via acceptShouldStop,
	// which unwinds Serve. Without this listener-close the only
	// listener-close path would be the ctx-cancel watcher, so a caller
	// that used Shutdown alone (without cancelling the Serve ctx) would
	// park Serve on Accept forever and silently break the "Shutdown
	// stops accepting new connections" contract.
	listener := e.listener

	e.listener = nil

	handles := make([]*sessionHandle, 0, len(e.sessions))
	for _, h := range e.sessions {
		handles = append(handles, h)
	}

	e.mu.Unlock()

	if listener != nil {
		closeErr := listener.Close()
		if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			e.logger.Debug("exporter shutdown listener close",
				slog.Any("err", closeErr))
		}
	}

	// Graceful: ask the kernel to release each active session's socket.
	// Disconnect writes -1 to usbip_sockfd; the kernel-side session
	// teardown emits the remove uevent that the
	// handler's pre-opened subscription observes. Failures are logged
	// but not fatal — the bounded drain falls through to force-close
	// + handle.cancel() so a kernel that ignores Disconnect still
	// unwinds. h.cancel() also fires immediately so a kernel path that
	// silently accepts Disconnect without emitting the remove uevent
	// (e.g. unit-test mocks, kernel without full netlink plumbing)
	// still terminates the parked handler via the Shutdown branch of
	// waitForSessionEnd.
	for _, h := range handles {
		err := e.kernel.Disconnect(ctx, h.session.BusID)
		if err != nil {
			e.logger.Warn("shutdown kernel disconnect",
				slog.Any("busid", h.session.BusID),
				slog.Any("err", err))
		}

		h.cancel()
	}

	drainCtx, cancel := e.applyShutdownBackstop(ctx)
	defer cancel()

	waitErr := e.waitSessionsBounded(drainCtx)

	// Tear down WatchSessions subscribers last so consumers see every
	// SessionEnded event published during drain before the channel
	// closes.
	e.closeAllSubscribers()

	return waitErr
}

// applyShutdownBackstop derives the ctx the drain actually waits on.
// When no backstop is configured (shutdownTimeout <= 0) the caller's
// ctx is returned unchanged with a no-op cancel. Otherwise the drain
// deadline is min(caller ctx deadline, now + shutdownTimeout) — the
// tighter of the two wins per the spec's "tighter wins" rule. If a
// caller deadline disabled the backstop entirely a caller with a
// generous ctx deadline would wait the caller-budget even when the
// configured timeout was orders of magnitude tighter.
//
// The returned cancel is always non-nil; when no new ctx is derived
// it is a no-op.
func (e *Exporter) applyShutdownBackstop(ctx context.Context) (context.Context, context.CancelFunc) {
	if e.shutdownTimeout <= 0 {
		return ctx, func() {}
	}

	backstopDeadline := e.clock.Now().Add(e.shutdownTimeout)

	if callerDeadline, ok := ctx.Deadline(); ok && callerDeadline.Before(backstopDeadline) {
		// Caller's deadline is already tighter than the backstop.
		// Return ctx unchanged so the caller's semantics apply.
		return ctx, func() {}
	}

	return context.WithDeadline(ctx, backstopDeadline)
}

// startServing transitions the Exporter from idle → serving. Returns
// ErrAlreadyShutdown when Shutdown has run or ErrServeAlreadyRunning
// when Serve is already running. The listener is stored on the
// Exporter so Shutdown can close it and unwind acceptLoop without
// depending on ctx-cancel (the documented contract is "Shutdown stops
// accepting new connections" — Shutdown alone must suffice).
func (e *Exporter) startServing(listener net.Listener) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.shutdown {
		return ErrAlreadyShutdown
	}

	if e.serving {
		return ErrServeAlreadyRunning
	}

	e.serving = true
	e.listener = listener

	return nil
}

// stopServing flips the serving flag back to false so a caller that
// wants to re-Serve after a non-shutdown Serve exit can do so. Clears
// the tracked listener so a follow-up Shutdown does not try to close
// a listener that is already gone.
func (e *Exporter) stopServing() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.serving = false
	e.listener = nil
}

// spawnCtxListenerCloser watches ctx and closes listener exactly once
// when ctx is cancelled. Returns a stop func so Serve can cancel the
// watcher cleanly on a normal return — without it, the watcher would
// leak a goroutine waiting on ctx forever. The stop func is idempotent
// via sync.Once so Serve can call it eagerly (before wg.Wait) without
// risking a close-of-closed-channel panic on the deferred second call.
func (e *Exporter) spawnCtxListenerCloser(ctx context.Context, listener net.Listener) func() {
	stop := make(chan struct{})

	e.wg.Go(func() {
		select {
		case <-ctx.Done():
			err := listener.Close()
			if err != nil && !errors.Is(err, net.ErrClosed) {
				e.logger.Debug("exporter listener close after ctx cancel",
					slog.Any("err", err))
			}
		case <-stop:
		}
	})

	return sync.OnceFunc(func() { close(stop) })
}

// acceptLoop pulls connections off listener and dispatches each to a
// fresh handler goroutine. Rate-limit rejections happen at the accept
// boundary so the token bucket never triggers handler work. Accept
// errors that indicate a closed listener return nil via
// acceptShouldStop; other errors are surfaced wrapped.
func (e *Exporter) acceptLoop(ctx context.Context, listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if acceptShouldStop(ctx, err) {
				return nil
			}

			return fmt.Errorf("exporter accept: %w", err)
		}

		if !e.acl.allow(conn.RemoteAddr()) {
			e.metrics.ExporterSessionAccepted(OutcomeRejectedACL)
			e.logger.Info("exporter accept rejected by ACL",
				slog.String("remote", conn.RemoteAddr().String()))

			closeConnLogging(conn, e.logger)

			continue
		}

		if !e.acceptLim.allow() {
			e.metrics.ExporterSessionAccepted(OutcomeRejectedRate)
			e.logger.Debug("exporter accept rate-limited",
				slog.String("remote", conn.RemoteAddr().String()))

			closeConnLogging(conn, e.logger)

			continue
		}

		e.sessionsWG.Go(func() {
			e.handleConn(ctx, conn)
		})
	}
}

// drainFuture returns the shared sessionsWG-drain channel. The first
// call spawns the single `sessionsWG.Wait(); close(sessionsDrained)`
// goroutine via sync.Once; subsequent calls return the same channel.
// Construction-time initialisation is not sufficient because Serve
// (and hence any sessionsWG.Go(...) calls) may not have happened yet
// — delaying the spawn until the first Shutdown keeps the waiter
// aligned with actual draining need.
func (e *Exporter) drainFuture() <-chan struct{} {
	e.sessionsDrainedOnce.Do(func() {
		go func() {
			e.sessionsWG.Wait()
			close(e.sessionsDrained)
		}()
	})

	return e.sessionsDrained
}

// shutdownResidualGrace is the extra wall-clock budget
// waitSessionsBounded allows after force-closing session conns before
// giving up and returning — long enough for a cooperative handler to
// unwind on the net.ErrClosed read, short enough to keep Shutdown's
// total deadline contract honest.
const shutdownResidualGrace = 100 * time.Millisecond

// waitSessionsBounded blocks until sessionsWG drains or ctx deadline
// expires. Returns a ctx-wrapped error on deadline expiry so callers
// distinguish graceful drain from forced cutoff.
//
// On deadline expiry the function force-closes every tracked session
// conn and allows a short residual grace for cooperative handlers to
// unwind on the resulting net.ErrClosed read. If the grace also
// expires the residual handler count is logged at Warn and the call
// returns the wrapped ctx error anyway — a truly stuck handler
// (one that ignores conn.Close) is accepted as a leaked goroutine
// tradeoff because Shutdown-blocks-forever is a worse failure mode
// than a bounded goroutine leak.
//
// The `sessionsWG.Wait()` observer is a single lazily-spawned
// goroutine shared across every Shutdown call. A truly-stuck handler
// keeps Wait() blocked forever; sessionsDrainedOnce gates the spawn
// so the waiter is allocated at most once per Exporter rather than
// leaking one parked waiter per Shutdown.
//
// After observing ctx.Done, a non-blocking re-check of `done` guards
// against the select-race where both channels are ready at once and
// Go picks the ctx branch. Without the re-check a completed drain
// can surface a spurious ctx-wrapped error to the caller. The same
// re-check applies to the post-grace observation so a drain that
// happened to complete alongside the grace expiry is reported as a
// successful drain rather than a force-close miss.
func (e *Exporter) waitSessionsBounded(ctx context.Context) error {
	done := e.drainFuture()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
	}

	// Non-blocking re-check: ctx.Done and done may have become ready
	// concurrently. Go's select picks randomly among ready cases, so
	// without this guard a completed drain can still surface a
	// ctx-wrapped error.
	select {
	case <-done:
		return nil
	default:
	}

	e.forceCloseSessionConns()

	// Residual grace uses wall-clock time directly — the Clock
	// abstraction exists for long-lived timers (backoffs, polls) that
	// tests drive with FakeClock. A 100ms post-force-close drain
	// window is too short to mock meaningfully, and every Clock
	// concrete in the project (RealClock, FakeClock) is already used
	// in tests that need Shutdown to return under a real ctx deadline.
	grace := time.NewTimer(shutdownResidualGrace)
	defer grace.Stop()

	select {
	case <-done:
		return fmt.Errorf("exporter shutdown: %w", ctx.Err())
	case <-grace.C:
	}

	// Symmetric non-blocking re-check at the grace boundary. If the
	// drain completed simultaneously with grace expiry, honour the
	// drain-completed branch.
	select {
	case <-done:
		return fmt.Errorf("exporter shutdown: %w", ctx.Err())
	default:
	}

	e.logger.Warn("exporter shutdown residual handlers did not drain",
		slog.Int("residual_sessions", e.countSessions()),
		slog.Duration("residual_grace", shutdownResidualGrace),
	)

	return fmt.Errorf("exporter shutdown: %w", ctx.Err())
}

// countSessions returns the current session bookkeeping count under
// the read lock. Used by waitSessionsBounded's residual-grace warning
// to report how many handlers refused to unwind after force-close.
func (e *Exporter) countSessions() int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return len(e.sessions)
}

// forceCloseSessionConns closes every tracked session conn so handlers
// parked in kernel.ExportOnConn error out and unwind sessionsWG. We
// lose the graceful TCP FIN the kernel would normally drive, but that
// is the only alternative to a hung drain.
func (e *Exporter) forceCloseSessionConns() {
	e.mu.RLock()

	conns := make([]net.Conn, 0, len(e.sessions))
	for _, h := range e.sessions {
		if h.conn != nil {
			conns = append(conns, h.conn)
		}
	}

	e.mu.RUnlock()

	for _, c := range conns {
		err := c.Close()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			e.logger.Debug("exporter force-close session conn",
				slog.Any("err", err))
		}
	}
}

// acceptShouldStop reports whether the accept error is a normal stop
// signal (ctx-driven or listener closed) vs a fatal error.
func acceptShouldStop(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}

	return errors.Is(err, net.ErrClosed)
}

// peerKeyFromAddr returns a stable key for the per-peer session
// counter. TCP addrs reduce to their IP literal; any other addr
// (notably pipeAddr in tests with no preset remote) falls back to the
// Addr's String() form so the tracker still distinguishes conns.
func peerKeyFromAddr(addr net.Addr) string {
	if addr == nil {
		return ""
	}

	if t, ok := addr.(*net.TCPAddr); ok && t != nil && t.IP != nil {
		return t.IP.String()
	}

	host := addr.String()

	h, _, err := net.SplitHostPort(host)
	if err == nil {
		return h
	}

	return host
}

// registerSession records a new accepted session in the handle map and
// increments the per-peer counter. Returns (nil, sentinel) when the
// global cap or per-peer cap is exhausted; the caller must close the
// conn without invoking the kernel in that case. conn is stored on the
// handle so Shutdown can force-close it when the drain deadline fires
// and the handler is parked in kernel.ExportOnConn.
func (e *Exporter) registerSession(sess domain.Session, peerKey string, conn net.Conn) (*sessionHandle, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.shutdown {
		return nil, ErrAlreadyShutdown
	}

	if e.cfg.maxSessions > 0 && len(e.sessions) >= e.cfg.maxSessions {
		return nil, ErrMaxSessionsExceeded
	}

	if e.cfg.maxSessionsPerPeer > 0 && e.perPeer[peerKey] >= e.cfg.maxSessionsPerPeer {
		return nil, ErrPerPeerLimitExceeded
	}

	h := &sessionHandle{
		session: sess,
		done:    make(chan struct{}),
		peerKey: peerKey,
		conn:    conn,
	}

	e.sessions[sess.ID] = h
	e.perPeer[peerKey]++

	return h, nil
}

// unregisterSession drops a session from the handle map and decrements
// the per-peer counter. Safe to call multiple times with the same id;
// the map delete and the counter decrement are both idempotent via the
// presence check.
func (e *Exporter) unregisterSession(id domain.SessionID) {
	e.mu.Lock()

	h, ok := e.sessions[id]
	if !ok {
		e.mu.Unlock()

		return
	}

	delete(e.sessions, id)

	if e.perPeer[h.peerKey] > 0 {
		e.perPeer[h.peerKey]--
	}

	if e.perPeer[h.peerKey] == 0 {
		delete(e.perPeer, h.peerKey)
	}

	n := len(e.sessions)

	e.mu.Unlock()

	h.cancel()

	// Push the updated session count to the gauge OUTSIDE the lock so a
	// slow prometheus accessor cannot back-pressure the session-close
	// path.
	e.metrics.ExporterSessionsActive(n)
}

