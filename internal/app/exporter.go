// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// defaultExportSessionStatusPollInterval is the sysfs backstop cadence
// used when usbip_host consumes peer EOF without emitting an exporter-
// side lifecycle uevent. Uevents remain the low-latency path.
const defaultExportSessionStatusPollInterval = 5 * time.Second

// Exporter is the use-case service that exports local USB devices via
// the usbip_host kernel surface. One Exporter is sufficient for a whole
// daemon process; construct via NewExporter and release via Shutdown.
// The zero value is not usable — NewExporter initialises required state.
//
// Every handle and counter is mutex-guarded so the accept loop, the
// per-session handler goroutines, Sessions(), and Shutdown() can all
// interleave safely under the race detector.
type Exporter struct {
	kernel          ExporterKernel
	sessionActivity exporterSessionActivity
	events          KernelEvents
	codec           ProtocolCodec
	clock           Clock
	logger          *slog.Logger
	newSessionID    func() (domain.SessionID, error)

	cfg                exporterLimits
	acceptLim          acceptLimiter
	acl                *aclChecker
	shutdownTimeout    time.Duration
	statusPollInterval time.Duration

	mu          sync.RWMutex
	shutdown    bool
	serving     bool
	listener    net.Listener
	serveCancel context.CancelFunc
	sessions    map[domain.SessionID]*sessionHandle
	perPeer     map[string]int
	subscribers []*sessionEventSubscriber
	// shutdownHandles retains the first Shutdown snapshot. Session
	// cleanup futures and their immutable errors remain observable to
	// every repeat caller even after handlers unregister from sessions.
	shutdownHandles []*sessionHandle

	// wg tracks the ctx-listener-closer goroutine spawned by Serve;
	// Serve waits on it before returning.
	wg lifecycleWaitGroup

	// sessionsWG tracks per-connection handler goroutines and their
	// handshake-timeout watchers. Serve deliberately does NOT wait on
	// sessionsWG (importer-lifecycle and exporter-daemon OpenSpec documents: Serve returns on ctx cancel; Shutdown
	// drains in-flight sessions bounded by its own ctx).
	sessionsWG lifecycleWaitGroup

	// sessionsDrainedOnce lazily captures sessionsWG's drain channel.
	// Multiple Shutdown calls share this drain future so bounded waits
	// observe the same session lifecycle without allocating waiter
	// goroutines. The once-init happens inside waitSessionsBounded — at
	// construction time Serve may not be up yet, so sessionsWG has no
	// participants to wait for.
	sessionsDrainedOnce sync.Once
	sessionsDrained     <-chan struct{}

	// acceptLoopExited is closed by Serve immediately after acceptLoop
	// returns. Shutdown waits on it before calling drainFuture so that
	// no sessionsWG.Go call can race with the captured drain channel.
	// Reset to a fresh channel by startServing on each Serve call; nil
	// before first Serve.
	acceptLoopExited chan struct{}
}

// sessionHandle is the per-session bookkeeping entry. done is closed
// exactly once when Shutdown signals the handler or final unregister
// completes. The handler observes done to synchronise with explicit
// shutdown. conn is the accepted net.Conn; Shutdown
// force-closes it to unwedge a handler parked in kernel.ExportOnConn
// when the drain deadline expires.
type sessionHandle struct {
	session   domain.Session
	done      chan struct{}
	closeOnce sync.Once
	peerKey   string
	conn      net.Conn
	// disconnectReason is the typed DisconnectReason recorded by
	// waitForSessionEnd at session termination. Stored via
	// atomic.Pointer[string] so endSession can read it without taking
	// e.mu (the publish path already holds an RLock and atomic ops
	// mate cleanly with the closed-set classification). Empty value
	// means waitForSessionEnd never recorded a typed reason — falls
	// back to the free-form string passed to endSession.
	disconnectReason atomic.Pointer[string]

	// handoffMu protects the explicit kernel handoff state machine.
	// Shutdown never blocks on the potentially-wedged ExportOnConn
	// call: an in-progress handoff is completed by the session handler,
	// which performs the deferred Disconnect itself if cancellation
	// won while the sysfs write was outstanding.
	handoffMu      sync.Mutex
	handoffState   kernelHandoffState
	cancelled      bool
	cleanupClaimed bool
	cleanupDone    chan struct{}
	cleanupErr     error
}

// kernelHandoffState records the only valid phases of a session's
// ExportOnConn lifecycle. The zero value deliberately means no handoff
// was attempted.
type kernelHandoffState uint8

const (
	kernelHandoffNotStarted kernelHandoffState = iota
	kernelHandoffInProgress
	kernelHandoffSucceeded
	kernelHandoffFailed
)

// cancel closes done exactly once. Safe to call from any goroutine.
func (h *sessionHandle) cancel() {
	h.closeOnce.Do(func() { close(h.done) })
}

// runHandoff invokes fn only if Shutdown has not cancelled the handle.
// It marks success only after fn returns nil. When cancellation arrives
// during fn, disconnectAfterHandoff is true and the completing session
// handler owns the exactly-once post-success Disconnect.
//
// ran is false when Shutdown won before the handoff started.
func (h *sessionHandle) runHandoff(fn func() error) (bool, bool, error) {
	h.handoffMu.Lock()

	if h.cancelled {
		h.handoffMu.Unlock()

		return false, false, nil
	}

	h.handoffState = kernelHandoffInProgress
	h.handoffMu.Unlock()

	err := fn()

	h.handoffMu.Lock()
	defer h.handoffMu.Unlock()

	if err != nil {
		h.handoffState = kernelHandoffFailed
		h.completeWithoutDisconnectLocked()

		return true, false, err
	}

	h.handoffState = kernelHandoffSucceeded
	if h.cancelled && h.claimCleanupLocked() {
		return true, true, nil
	}

	return true, false, nil
}

// signalCancel atomically marks the handle cancelled and claims the
// exactly-once Disconnect only when ExportOnConn has already succeeded.
// An in-progress handoff returns false; runHandoff claims and performs
// cleanup if that call later succeeds.
func (h *sessionHandle) signalCancel() bool {
	h.handoffMu.Lock()
	defer h.handoffMu.Unlock()

	h.cancelled = true

	switch h.handoffState {
	case kernelHandoffSucceeded:
		return h.claimCleanupLocked()
	case kernelHandoffNotStarted, kernelHandoffFailed:
		h.completeWithoutDisconnectLocked()
	case kernelHandoffInProgress:
		// runHandoff claims cleanup after the outstanding kernel write
		// resolves, and only if that write actually succeeded.
	}

	return false
}

// claimCleanupLocked assigns the one cleanup owner. handoffMu must be
// held. true means the caller must perform Disconnect and then call
// finishCleanup; false means another terminal path already owns it.
func (h *sessionHandle) claimCleanupLocked() bool {
	if h.cleanupClaimed {
		return false
	}

	h.cleanupClaimed = true

	return true
}

// completeWithoutDisconnect marks natural peer completion or a
// pre-handoff terminal path. It cannot suppress an already-claimed
// Shutdown Disconnect because ownership is decided under handoffMu.
func (h *sessionHandle) completeWithoutDisconnect() bool {
	h.handoffMu.Lock()
	defer h.handoffMu.Unlock()

	return h.completeWithoutDisconnectLocked()
}

func (h *sessionHandle) completeWithoutDisconnectLocked() bool {
	if !h.claimCleanupLocked() {
		return false
	}

	close(h.cleanupDone)

	return true
}

// finishCleanup publishes the immutable result of the claimed kernel
// Disconnect and releases every initial or repeated Shutdown waiter.
func (h *sessionHandle) finishCleanup(err error) {
	h.handoffMu.Lock()
	defer h.handoffMu.Unlock()

	h.cleanupErr = err
	close(h.cleanupDone)
}

func (h *sessionHandle) completedCleanupResult() (bool, error) {
	select {
	case <-h.cleanupDone:
		h.handoffMu.Lock()
		defer h.handoffMu.Unlock()

		return true, h.cleanupErr
	default:
		return false, nil
	}
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
// error (ErrACLInvalid or ErrAcceptRateLimitInvalid). Missing-dependency
// errors still panic — those are programming bugs, not runtime conditions.
func NewExporterWithError(opts ...ExporterOption) (*Exporter, error) {
	cfg := exporterConfig{
		clock:        RealClock{},
		logger:       slog.Default(),
		newSessionID: domain.NewSessionID,
	}

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

	if cfg.acceptRateLimitSet {
		err = ValidateAcceptRateLimit(cfg.acceptRateLimit)
		if err != nil {
			return nil, err
		}
	}

	sessionActivity, _ := cfg.kernel.(exporterSessionActivity)

	return &Exporter{
		kernel:             cfg.kernel,
		sessionActivity:    sessionActivity,
		events:             cfg.events,
		codec:              cfg.codec,
		clock:              cfg.clock,
		logger:             cfg.logger,
		newSessionID:       cfg.newSessionID,
		cfg:                resolveExporterLimits(&cfg),
		acceptLim:          newAcceptLimiter(resolveAcceptRate(&cfg), resolveAcceptBurst(&cfg)),
		acl:                acl,
		shutdownTimeout:    cfg.shutdownTimeout,
		statusPollInterval: resolveExporterStatusPollInterval(cfg.statusPollInterval),
		sessions:           make(map[domain.SessionID]*sessionHandle),
		perPeer:            make(map[string]int),
	}, nil
}

func resolveExporterStatusPollInterval(interval time.Duration) time.Duration {
	if interval == 0 {
		return defaultExportSessionStatusPollInterval
	}

	return interval
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

	if cfg.codec == nil {
		panic("app.NewExporter: ProtocolCodec is required (use WithExporterCodec)")
	}
}

// Bind delegates to kernel.Bind per exporter-daemon OpenSpec. Errors are
// wrapped with the busid so callers that bind many devices can
// identify which failed. Every terminal branch logs a structured slog
// record with an outcome field so journald queries can filter by
// outcome without parsing free-form messages.
//
// The boundary guard rejects malformed BusID values BEFORE any
// kernel-adapter call. Callers that construct a BusID by raw string
// conversion (bypassing ParseBusID) cannot smuggle a path-traversal
// sequence into the sysfs writes the kernel adapter performs. The
// CLI re-validates earlier, but library embedders enter through this
// surface and must hit the same gate.
func (e *Exporter) Bind(ctx context.Context, busID domain.BusID) error {
	if !busID.IsValid() {
		err := fmt.Errorf("bind %q: %w", busID, domain.ErrBusIDInvalid)

		e.logger.Warn("exporter bind rejected",
			slog.Any("busid", busID),
			slog.String("outcome", string(BindOutcomeError)),
			slog.Any("err", err))

		return err
	}

	err := e.kernel.Bind(ctx, busID)
	if err != nil {
		e.logger.Warn("exporter bind failed",
			slog.Any("busid", busID),
			slog.String("outcome", string(classifyBindError(err))),
			slog.Any("err", err))

		return fmt.Errorf("bind %s: %w", busID, err)
	}

	e.logger.Info("exporter bind",
		slog.Any("busid", busID),
		slog.String("outcome", string(BindOutcomeOK)))

	return nil
}

// Unbind delegates to kernel.Unbind per exporter-daemon OpenSpec. Errors are
// wrapped with the busid. Outcome is logged structurally per Bind's
// contract. The same BusID validity gate as Bind applies — see Bind
// for the boundary rationale.
func (e *Exporter) Unbind(ctx context.Context, busID domain.BusID) error {
	if !busID.IsValid() {
		err := fmt.Errorf("unbind %q: %w", busID, domain.ErrBusIDInvalid)

		e.logger.Warn("exporter unbind rejected",
			slog.Any("busid", busID),
			slog.String("outcome", string(UnbindOutcomeError)),
			slog.Any("err", err))

		return err
	}

	err := e.kernel.Unbind(ctx, busID)
	if err != nil {
		e.logger.Warn("exporter unbind failed",
			slog.Any("busid", busID),
			slog.String("outcome", string(classifyUnbindError(err))),
			slog.Any("err", err))

		return fmt.Errorf("unbind %s: %w", busID, err)
	}

	e.logger.Info("exporter unbind",
		slog.Any("busid", busID),
		slog.String("outcome", string(UnbindOutcomeOK)))

	return nil
}

// classifyBindError maps a kernel Bind error onto the operations-observability OpenSpec
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

// ListAvailable forwards to kernel.ListLocalDevices per exporter-daemon OpenSpec. The
// kernel adapter is the authoritative source; the Exporter does not
// maintain a cache of its own.
func (e *Exporter) ListAvailable(ctx context.Context) ([]domain.Device, error) {
	devs, err := e.kernel.ListLocalDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list local devices: %w", err)
	}

	return devs, nil
}

// ListExported returns devices currently bound to usbip-host that
// are not actively claimed by an importer (SDEV_ST_USED excluded).
// This is the semantic answer to "what devices does this daemon
// have BOUND right now" — distinct from ListAvailable which dumps
// every local USB device regardless of bind state. Consumed by the
// status-socket BoundDevices endpoint and by any caller that needs
// to mirror the wire-side OP_REP_DEVLIST view.
func (e *Exporter) ListExported(ctx context.Context) ([]domain.Device, error) {
	devs, err := e.kernel.ListExportedDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list exported devices: %w", err)
	}

	return devs, nil
}

// Serve runs the accept loop per exporter-daemon OpenSpec. Each accepted connection
// is dispatched to a per-connection goroutine via handleConn; the
// accept loop returns when ctx is cancelled, the listener returns a
// permanent error, or Shutdown is called. Returns ErrAlreadyShutdown
// if invoked after Shutdown. Concurrent calls are safe, but at most one may
// own the accept-loop reservation; an overlapping call receives
// ErrServeAlreadyRunning.
func (e *Exporter) Serve(ctx context.Context, listener net.Listener) error {
	return e.serveWithListenerFactory(ctx, func(context.Context) (net.Listener, error) {
		return listener, nil
	})
}

// ServeWithListenerFactory reserves the Exporter's terminal/overlap state
// before invoking factory. The public facade uses this internal-module seam so
// ListenAndServe cannot bind a socket and only then discover that Shutdown or
// another Serve already won. factory MUST honor the supplied context.
func (e *Exporter) ServeWithListenerFactory(
	ctx context.Context,
	factory func(context.Context) (net.Listener, error),
) error {
	return e.serveWithListenerFactory(ctx, factory)
}

// Shutdown stops accepting new connections and drains in-flight
// session handlers. Per importer-lifecycle and exporter-daemon OpenSpec documents: new accepts are refused, existing
// handlers are signalled to exit, and the call waits for them bounded
// by the provided ctx deadline. Returns nil when the drain completes
// before the deadline; a ctx.Err-wrapped error when it does not. Repeated
// calls observe the retained per-session cleanup results without issuing
// another Disconnect.
//
// When the caller's ctx carries no deadline and the Exporter was built
// with WithExporterShutdownTimeout(d>0), the option's value is applied
// as an internal backstop deadline so a wedged ExportOnConn cannot
// block Shutdown forever despite a configured timeout.
func (e *Exporter) Shutdown(ctx context.Context) error {
	state := e.captureShutdownState()

	if state.firstShutdown && state.serveCancel != nil {
		state.serveCancel()
	}

	if state.firstShutdown && state.listener != nil {
		closeErr := state.listener.Close()
		if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			e.logger.Debug("exporter shutdown listener close",
				slog.Any("err", closeErr))
		}
	}

	// Derive the backstopped drainCtx FIRST so the per-session
	// Disconnect goroutines below can carry it. Using the original
	// caller ctx instead would let a Disconnect outlive the configured
	// shutdownTimeout backstop even when waitDisconnectBounded fires.
	drainCtx, cancel := e.applyShutdownBackstop(ctx)
	defer cancel()

	if state.firstShutdown {
		e.spawnGracefulDisconnects(drainCtx, state.handles)
	}

	// Wait for acceptLoop to exit so no sessionsWG.Go call can race
	// with drainFuture's sessionsWG drain-channel capture. acceptLoopExited
	// is nil when Shutdown is called without a prior Serve; the channel
	// is already closed when Shutdown is called after Serve returns.
	if state.acceptLoopExited != nil {
		<-state.acceptLoopExited
	}

	waitErr := e.waitSessionsBounded(drainCtx)
	cleanupErr := e.waitCleanupBounded(drainCtx, state.handles)

	// Tear down WatchSessions subscribers last so consumers see every
	// SessionEnded event published during drain before the channel
	// closes.
	e.closeAllSubscribers()

	return errors.Join(waitErr, cleanupErr)
}

func (e *Exporter) serveWithListenerFactory(
	ctx context.Context,
	factory func(context.Context) (net.Listener, error),
) error {
	serveCtx, cancel := context.WithCancel(ctx)

	acceptLoopExited, err := e.startServing(cancel)
	if err != nil {
		cancel()

		return err
	}

	defer e.stopServing()

	// signalAcceptExited closes acceptLoopExited exactly once via Once so
	// a panic inside acceptLoop (or spawnCtxListenerCloser) still unblocks
	// any concurrent Shutdown waiting on that channel.
	var acceptExitedOnce sync.Once

	signalAcceptExited := func() {
		acceptExitedOnce.Do(func() { close(acceptLoopExited) })
	}
	defer signalAcceptExited()

	listener, err := factory(serveCtx)
	if err != nil {
		return err
	}

	if listener == nil {
		return errListenerFactoryNil
	}

	err = e.installServingListener(listener)
	if err != nil {
		_ = listener.Close()

		return err
	}

	stopWatcher := e.spawnCtxListenerCloser(serveCtx, listener)

	loopErr := e.acceptLoop(serveCtx, listener)

	// Signal Shutdown early — acceptLoop has exited, no further
	// sessionsWG.Go calls will occur. The defer above covers panics.
	signalAcceptExited()

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
	// importer-lifecycle and exporter-daemon OpenSpec documents. If Serve also waited on sessionsWG here, an in-flight
	// ExportOnConn would block Serve's return past ctx cancel.
	e.wg.Wait()

	return loopErr
}

// shutdownState bundles every field Shutdown must snapshot while
// holding e.mu. Returned by captureShutdownState so the Shutdown
// caller does not need to remember a five-tuple of distinct names.
type shutdownState struct {
	firstShutdown    bool
	handles          []*sessionHandle
	listener         net.Listener
	serveCancel      context.CancelFunc
	acceptLoopExited <-chan struct{}
}

// captureShutdownState atomically transitions the Exporter into the
// shutdown state and snapshots every field Shutdown must read while
// holding e.mu: the active listener, the acceptLoop completion channel,
// and the first shutdown's live Session handle set. firstShutdown is true
// only for the call that flips e.shutdown; every call receives a copy of
// the same retained handles so it can observe their immutable results.
func (e *Exporter) captureShutdownState() shutdownState {
	e.mu.Lock()
	defer e.mu.Unlock()

	state := shutdownState{
		firstShutdown:    !e.shutdown,
		listener:         e.listener,
		serveCancel:      e.serveCancel,
		acceptLoopExited: e.acceptLoopExited,
	}

	e.shutdown = true

	if state.firstShutdown {
		e.listener = nil

		e.shutdownHandles = make([]*sessionHandle, 0, len(e.sessions))
		for _, h := range e.sessions {
			e.shutdownHandles = append(e.shutdownHandles, h)
		}
	}

	state.handles = append([]*sessionHandle(nil), e.shutdownHandles...)

	return state
}

// spawnGracefulDisconnects fires the per-handle cancel/disconnect
// transition for the firstShutdown path: signalCancel returns whether
// ExportOnConn has already succeeded. Disconnect is only scheduled for
// handed-off sessions;
// a handle that never reached ExportOnConn left no kernel state to
// clean up. signalCancel-then-cancel ordering is deliberate: cancel()
// closes done unconditionally so a handler blocked in
// waitForSessionEnd unwinds either way. Each claimed handle owns its own
// cleanup future, which concurrent and sequential Shutdown calls share.
func (e *Exporter) spawnGracefulDisconnects(drainCtx context.Context, handles []*sessionHandle) {
	for _, h := range handles {
		handedOff := h.signalCancel()

		h.cancel()

		if !handedOff {
			continue
		}

		go e.disconnectSession(drainCtx, h)
	}
}

// disconnectSession performs the kernel mutation claimed under the
// handle's handoff mutex, then publishes the wrapped immutable result.
func (e *Exporter) disconnectSession(ctx context.Context, h *sessionHandle) {
	err := e.kernel.Disconnect(ctx, h.session.BusID)
	if err != nil {
		err = fmt.Errorf("disconnect session %s (%s): %w", h.session.ID, h.session.BusID, err)
	}

	// Publish the terminal future before ancillary logging. Besides making
	// cleanup ownership observable at the earliest correct point, this gives
	// tests and operators a sound ordering: once the error record is emitted,
	// every Shutdown waiter can already observe the stored result.
	h.finishCleanup(err)

	if err != nil {
		e.logger.Warn("shutdown kernel disconnect",
			slog.Any("busid", h.session.BusID),
			slog.Any("err", err))
	}
}

// waitCleanupBounded waits each retained per-session future against
// the shared drain deadline and joins every completed error. When the
// deadline wins it performs a non-blocking sweep so failures from
// other handles that already completed are not hidden by one wedge.
func (e *Exporter) waitCleanupBounded(ctx context.Context, handles []*sessionHandle) error {
	var errs []error

	for idx, h := range handles {
		timedOut, err := waitSessionCleanup(ctx, h)
		if err != nil {
			errs = append(errs, err)
		}

		if !timedOut {
			continue
		}

		errs = append(errs, fmt.Errorf("shutdown session cleanup: %w", ctx.Err()))
		errs = append(errs, completedCleanupErrors(handles[idx+1:])...)

		return errors.Join(errs...)
	}

	return errors.Join(errs...)
}

// waitSessionCleanup waits one immutable per-session future. timedOut is true
// only when ctx won and a final non-blocking recheck still found the future
// pending; a completed future wins every cancellation tie.
func waitSessionCleanup(ctx context.Context, h *sessionHandle) (bool, error) {
	complete, resultErr := h.completedCleanupResult()
	if complete {
		return false, resultErr
	}

	select {
	case <-h.cleanupDone:
		_, resultErr = h.completedCleanupResult()

		return false, resultErr
	case <-ctx.Done():
	}

	complete, resultErr = h.completedCleanupResult()
	if complete {
		return false, resultErr
	}

	return true, nil
}

func completedCleanupErrors(handles []*sessionHandle) []error {
	errs := make([]error, 0, len(handles))

	for _, h := range handles {
		complete, err := h.completedCleanupResult()
		if complete && err != nil {
			errs = append(errs, err)
		}
	}

	return errs
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

	// WithTimeout automatically preserves a tighter parent deadline and
	// uses the runtime's monotonic clock. The injected application clock
	// is intentionally not mixed with context's wall-clock deadlines.
	return context.WithTimeout(ctx, e.shutdownTimeout)
}

// startServing transitions the Exporter from idle → serving before listener
// creation. It returns ErrAlreadyShutdown when Shutdown has run or
// ErrServeAlreadyRunning when Serve is already running. cancel lets Shutdown
// stop an in-flight listener factory; once a listener is installed, Shutdown
// also closes it to unwind acceptLoop.
func (e *Exporter) startServing(cancel context.CancelFunc) (chan struct{}, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.shutdown {
		return nil, ErrAlreadyShutdown
	}

	if e.serving {
		return nil, ErrServeAlreadyRunning
	}

	acceptLoopExited := make(chan struct{})

	e.serving = true
	e.listener = nil
	e.serveCancel = cancel
	e.acceptLoopExited = acceptLoopExited

	return acceptLoopExited, nil
}

// installServingListener publishes the factory result for Shutdown. A
// concurrent Shutdown may win while the context-aware factory is returning; in
// that case the listener is rejected and the caller closes it immediately.
func (e *Exporter) installServingListener(listener net.Listener) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.shutdown {
		return ErrAlreadyShutdown
	}

	e.listener = listener

	return nil
}

// stopServing flips the serving flag back to false so a caller that
// wants to re-Serve after a non-shutdown Serve exit can do so. Clears
// the tracked listener so a follow-up Shutdown does not try to close
// a listener that is already gone.
func (e *Exporter) stopServing() {
	e.mu.Lock()

	cancel := e.serveCancel

	e.serving = false
	e.listener = nil
	e.serveCancel = nil

	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
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
			e.logger.Info("exporter accept rejected by ACL",
				slog.String("remote", conn.RemoteAddr().String()),
				slog.String("outcome", string(OutcomeRejectedACL)))

			closeConnLogging(conn, e.logger)

			continue
		}

		if !e.acceptLim.allow() {
			e.logger.Debug("exporter accept rate-limited",
				slog.String("remote", conn.RemoteAddr().String()),
				slog.String("outcome", string(OutcomeRejectedRate)))

			closeConnLogging(conn, e.logger)

			continue
		}

		e.sessionsWG.Go(func() {
			e.handleConn(ctx, conn)
		})
	}
}

// drainFuture returns the shared sessionsWG-drain channel. The first
// call captures the lifecycle wait group's current drain channel via
// sync.Once; subsequent calls return the same channel. Construction-
// time initialisation is not sufficient because Serve (and hence any
// sessionsWG.Go(...) calls) may not have happened yet — delaying the
// capture until the first Shutdown keeps the future aligned with actual
// draining need.
func (e *Exporter) drainFuture() <-chan struct{} {
	e.sessionsDrainedOnce.Do(func() {
		e.sessionsDrained = e.sessionsWG.DoneChan()
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
// The sessionsWG observer is a single lazily-captured drain channel
// shared across every Shutdown call. A truly-stuck handler keeps that
// channel open forever, but no additional waiter goroutine is allocated
// by the bounded wait path.
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

	e.logger.Warn(
		"exporter shutdown residual handlers did not drain",
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

	// Reject a second concurrent session for the same busid before the
	// kernel handoff. Without this guard two importers racing the same
	// device both pass lookupExportedDevice, both get a success
	// OP_REP_IMPORT, and the kernel rejects the second sockfd write —
	// leaving the second importer with a contradictory protocol exchange
	// (success reply, then a closed conn). Surfacing the collision as
	// ErrDeviceAlreadyBound lets serveImport reply ST_DEV_BUSY before
	// any handoff.
	for _, existing := range e.sessions {
		if existing.session.BusID == sess.BusID {
			return nil, fmt.Errorf("%w: busid %s", domain.ErrDeviceAlreadyBound, sess.BusID)
		}
	}

	h := &sessionHandle{
		session:     sess,
		done:        make(chan struct{}),
		cleanupDone: make(chan struct{}),
		peerKey:     peerKey,
		conn:        conn,
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

	e.mu.Unlock()

	h.cancel()
}
