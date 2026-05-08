// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/prometheus/client_golang/prometheus"
)

// statusReadyTimeout bounds how long runDaemon waits for the status
// server goroutine to report its listener is accepting; exceeding this
// budget logs a warning but does not abort startup.
const statusReadyTimeout = 3 * time.Second

// errDrainRequested cancels the Serve-ctx when POST /drain fires; the
// error is surfaced to operators via context.Cause at shutdown so logs
// distinguish drain from SIGTERM.
var errDrainRequested = errors.New("drain requested")

// runDaemon composes the full usbipd runtime: pick (or accept) the
// accept-path listener, build a pkg/usbip.Exporter from Config, start
// the status UDS server when configured, and block on Serve until ctx
// is cancelled or drain is requested via the status endpoint. On
// shutdown a bounded Exporter.Shutdown drains in-flight sessions under
// cfg.ShutdownTimeout per §7.7.
func runDaemon(ctx context.Context, cfg *Config) error {
	log := loggerFromCtx(ctx)
	if log == nil {
		log = slog.Default()
	}

	activated := systemdActivated()

	listener, err := listenOrActivation(ctx, cfg)
	if err != nil {
		return fmt.Errorf("accept-path listener: %w", err)
	}

	defer func() {
		_ = listener.Close()
	}()

	// Build the Prometheus registry before constructing the exporter so
	// the metric bundle is wired into the Serve/Shutdown path on the
	// first session. The registry stays nil-safe when --metrics-addr is
	// empty: the exporter's metricsRegisterer option simply leaves the
	// bundle in its nop state.
	metricsReg := prometheus.NewRegistry()

	exp, err := buildExporter(cfg, log, metricsReg)
	if err != nil {
		return err
	}

	// Serve runs under a child context that drain can cancel with a
	// labelled cause. The parent ctx still propagates SIGTERM through
	// the same cancel func. The status server lives on its OWN child
	// ctx so it stays up through the Serve drain but is wound down
	// after Serve returns; otherwise completeShutdown would wait for a
	// server that never sees ctx.Done().
	serveCtx, cancelServe := context.WithCancelCause(ctx)
	defer cancelServe(nil)

	statusCtx, cancelStatus := context.WithCancel(ctx)
	defer cancelStatus()

	src := newStatusExporter(exp, listener, activated)

	metricsStop, err := maybeStartMetricsServer(ctx, cfg, log, metricsReg, src)
	if err != nil {
		return err
	}

	// Two-stage LIFO shutdown: the metrics HTTP server must go down
	// BEFORE exporter drain so /readyz and /metrics cannot hand out
	// stale "accepting=true" or fresh session counters while the
	// exporter is winding down. LIFO means the LAST-registered defer
	// runs FIRST, so we register exp-drain first, then metrics-stop.
	defer drainExporter(ctx, cfg, exp, log)

	if metricsStop != nil {
		defer shutdownMetricsServer(ctx, metricsStop, cfg.ShutdownTimeout, log)
	}

	src.setDrain(func() {
		cancelServe(errDrainRequested)
	})

	statusErrCh, statusBound, statusStartErr := maybeStartStatusServer(statusCtx, cfg, log, src)
	if statusStartErr != nil {
		return statusStartErr
	}

	// The unlink defer registers only after the status goroutine
	// confirms a successful bind. A daemon that loses the bind race
	// (errAlreadyRunning) has no ownership of the on-disk UDS file and
	// must leave it in place for the incumbent peer.
	if statusBound {
		defer func() { _ = os.Remove(cfg.StatusSocket) }()
	}

	log.Info("usbipd accepting connections",
		slog.String("addr", listener.Addr().String()),
		slog.Bool("activation", activated))

	// Accepting flips true on the FIRST successful Accept: an accept-
	// intercepting wrapper fires src.markAccepting(true) exactly once.
	// Setting the flag true BEFORE Serve entered the accept loop would
	// let /readyz report 200 during the brief window where the accept
	// loop might still fail to arm.
	serveErr := exp.Serve(serveCtx, wrapListenerFirstAccept(listener, src))

	src.markAccepting(false)

	// Wind down the status UDS after Serve returns. The server is kept
	// alive through Serve so `usbipd drain` can poll sessions=[] during
	// the drain window; cancelling it here lets completeShutdown
	// observe statusErrCh without racing the drain.
	cancelStatus()

	return completeShutdown(ctx, serveCtx, cfg, log, serveErr, statusErrCh)
}

// firstAcceptListener decorates a net.Listener with a one-shot hook
// that fires when the accept loop ENTERS its first Accept call. The
// readiness semantic is "I will accept if something connects", not
// "I have accepted ≥1" — firing on first successful Accept return
// would leave an idle daemon (no clients) stuck at /readyz=503
// forever. Firing on entry lets /readyz flip 200 as soon as the
// accept loop is armed.
type firstAcceptListener struct {
	net.Listener

	once sync.Once
	hook func()
}

// wrapListenerFirstAccept returns a listener that fires
// src.markAccepting(true) when the accept loop first enters Accept.
// Production uses this once per Serve; tests construct statusExporter
// directly and drive markAccepting via their own path.
func wrapListenerFirstAccept(lis net.Listener, src *statusExporter) net.Listener {
	return wrapListenerFirstAcceptWithHook(lis, func() {
		src.markAccepting(true)
	})
}

// wrapListenerFirstAcceptWithHook is the hook-observable variant tests
// use to assert the firstAcceptListener lifecycle without wiring a
// statusExporter. Production callers go through wrapListenerFirstAccept.
func wrapListenerFirstAcceptWithHook(lis net.Listener, hook func()) net.Listener {
	return &firstAcceptListener{
		Listener: lis,
		hook:     hook,
	}
}

// Accept calls through to the wrapped listener and fires the hook
// BEFORE blocking on the inner Accept so the readiness flip lands as
// soon as the accept loop is armed. Firing the hook only on
// successful return would leave an idle daemon stuck at /readyz=503
// forever. Preserves the listener's original error on the return
// path so isExpectedServeExit still recognises graceful shutdowns.
func (l *firstAcceptListener) Accept() (net.Conn, error) {
	l.once.Do(l.hook)

	conn, err := l.Listener.Accept()
	if err != nil {
		return conn, err //nolint:wrapcheck // preserving the listener's original error for isExpectedServeExit
	}

	return conn, nil
}

// drainExporter is the deferred Exporter.Shutdown callpoint. Extracted
// from completeShutdown so the deferred-stack ordering — metrics HTTP
// server down FIRST, exporter drain SECOND — is visible in runDaemon
// without hiding the ordering inside completeShutdown's branch-heavy
// body.
func drainExporter(
	parentCtx context.Context,
	cfg *Config,
	exp *usbip.Exporter,
	log *slog.Logger,
) {
	shutdownCtx, cancel := context.WithTimeout(
		context.WithoutCancel(parentCtx), cfg.ShutdownTimeout)
	defer cancel()

	err := exp.Shutdown(shutdownCtx)
	if err != nil {
		log.Warn("exporter shutdown returned error", slog.Any("err", err))
	}
}

// buildExporter constructs the pkg/usbip.Exporter from cfg. The
// translation of Config fields to With* options lives here so runDaemon
// stays focused on lifecycle rather than option plumbing. reg is the
// Prometheus registry the exporter publishes to; a nil reg opts into
// the no-op metrics bundle.
//
// When reg is non-nil, WithExporterBuildInfo stamps the usbip_build_info
// gauge during construction. This replaces the previous pattern of
// calling Metrics.SetBuildInfo from maybeStartMetricsServer, which
// built a SECOND bundle against the same registry and panicked on
// duplicate collector registration.
func buildExporter(
	cfg *Config, log *slog.Logger, reg prometheus.Registerer,
) (*usbip.Exporter, error) {
	opts := []usbip.ExporterOption{
		usbip.WithExporterLogger(log),
		usbip.WithExporterMaxSessions(cfg.MaxSessions),
		usbip.WithExporterMaxSessionsPerPeer(cfg.MaxSessionsPerPeer),
		usbip.WithExporterAcceptRateLimit(cfg.AcceptRateLimit),
		usbip.WithExporterMaxHandshakeBytes(cfg.MaxHandshakeBytes),
		usbip.WithExporterHandshakeTimeout(cfg.HandshakeTimeout),
		usbip.WithExporterShutdownTimeout(cfg.ShutdownTimeout),
	}

	if reg != nil {
		opts = append(opts,
			usbip.WithExporterMetricsRegisterer(reg),
			usbip.WithExporterBuildInfo(version, commit, runtime.Version()))
	}

	if len(cfg.AllowCIDR) > 0 {
		opts = append(opts, usbip.WithExporterAllowCIDR(cfg.AllowCIDR...))
	}

	exp, err := usbip.NewExporter(opts...)
	if err != nil {
		return nil, fmt.Errorf("construct exporter: %w", err)
	}

	return exp, nil
}

// maybeStartMetricsServer spins the Prometheus HTTP endpoint on a
// separate listener when cfg.MetricsAddr is non-empty. Empty disables
// the endpoint — the returned stop func is nil and runDaemon skips the
// deferred shutdown branch. The readiness checker consults the
// statusExporter for kernel-module state and accepting flag; the
// "status socket writable" bit reads cfg.StatusSocket (empty means
// "disabled, treat as writable for the purposes of readiness").
func maybeStartMetricsServer(
	ctx context.Context,
	cfg *Config,
	log *slog.Logger,
	reg *prometheus.Registry,
	src *statusExporter,
) (func(context.Context) error, error) {
	if cfg.MetricsAddr == "" {
		return nil, nil //nolint:nilnil // documented "addr empty = disabled" signal
	}

	// Build-info is stamped at Exporter construction via
	// WithExporterBuildInfo; the metrics-HTTP server just serves
	// promhttp.HandlerFor(reg) on the already-populated registry. A
	// second MustNewMetrics call here would re-register the §11.5.5
	// collectors and panic on duplicate registration.

	probe := newDaemonReadinessProbe(cfg, src)

	stop, err := startMetricsServer(ctx, cfg.MetricsAddr, reg,
		newReadinessChecker(probe))
	if err != nil {
		return nil, err
	}

	log.Info("metrics endpoint listening",
		slog.String("addr", cfg.MetricsAddr))

	return stop, nil
}

// newDaemonReadinessProbe returns the readinessProbe consulted by
// /readyz. It polls KernelModules via the cached statusExporter path,
// reads the listenerBound + accepting flags from the exporter, and
// consults syscall.Access(W_OK) on the status socket path. The four
// inputs mirror the §11.5.5 readiness contract.
func newDaemonReadinessProbe(cfg *Config, src *statusExporter) readinessProbe {
	return func(ctx context.Context) readinessState {
		mods, modErr := src.KernelModules(ctx)
		if modErr != nil {
			// Fall through with an empty map: every "required" module
			// read returns the zero ModuleState (Unknown) and
			// readinessState.ready() correctly reports not-ready.
			mods = map[string]usbip.ModuleState{}
		}

		return readinessState{
			ListenerBound:  src.listenerBound.Load(),
			Accepting:      src.accepting.Load(),
			StatusWritable: statusSocketWritable(cfg.StatusSocket),
			Modules:        mods,
		}
	}
}

// shutdownMetricsServer performs the deferred HTTP shutdown under a
// timeout derived from parentCtx so a wedged handler can't hang daemon
// exit. Errors are logged at warn level — the process is unwinding and
// cannot surface them via the main error path.
func shutdownMetricsServer(
	parentCtx context.Context,
	stop func(context.Context) error,
	timeout time.Duration,
	log *slog.Logger,
) {
	sctx, cancel := context.WithTimeout(
		context.WithoutCancel(parentCtx), timeout)
	defer cancel()

	err := stop(sctx)
	if err != nil {
		log.Warn("metrics server shutdown returned error", slog.Any("err", err))
	}
}

// statusSocketWritable reports whether the daemon can drive status
// writes at readiness-check time. An empty path (status disabled)
// counts as "writable" because there is nothing to gate on. A non-
// empty path must exist AND be writable by the current uid.
//
// The check uses unix.Access(path, unix.W_OK) rather than os.Stat:
// os.Stat only confirms the inode exists, which lets /readyz report
// 200 in the case where the UDS file is present but owned by a
// different user (0400 root:root, for instance). Access consults the
// kernel's permission logic and returns an error whenever a write
// would be refused, so /readyz stays red until the daemon actually
// has the rights the §7.7 status write path needs.
//
// We prefer golang.org/x/sys/unix.Access over syscall.Access because
// the stdlib syscall package does not export the W_OK constant on
// Linux; unix.Access does, and the project already depends on x/sys
// for other kernel-facing plumbing.
func statusSocketWritable(path string) bool {
	if path == "" {
		return true
	}

	return unix.Access(path, unix.W_OK) == nil
}

// serveStatusFn is the indirection through which runDaemon launches
// the status UDS server. Production wiring is serveStatus itself; unit
// tests override this variable to inject a fake that deliberately
// SKIPS the listener/unlink cleanup so the "run() owns the unlink"
// invariant can be RED-tested. Protected by serveStatusFnMu so
// parallel tests replacing the hook are race-free.
var (
	serveStatusFn   = serveStatus
	serveStatusFnMu sync.RWMutex
)

// currentServeStatusFn returns the active status-server implementation
// under a read lock so tests swapping serveStatusFn don't race the
// production path's reads.
func currentServeStatusFn() func(
	ctx context.Context, path, group string,
	src statusSource, started chan<- struct{},
) error {
	serveStatusFnMu.RLock()
	defer serveStatusFnMu.RUnlock()

	return serveStatusFn
}

// maybeStartStatusServer spins the §7.7 status UDS in a goroutine when
// cfg.StatusSocket is non-empty and returns:
//
//   - statusErrCh: receive-only error channel the caller monitors
//     during shutdown; nil when the endpoint is disabled.
//   - bound: true when the status goroutine successfully bound the
//     UDS; false when the endpoint is disabled or when bind failed
//     (e.g. errAlreadyRunning against a live peer).
//   - startErr: a terminal bind error that must abort runDaemon
//     before it registers ownership-sensitive cleanup such as
//     unlinking the UDS file.
//
// The function synchronises on the goroutine's start signal so the
// caller can decide, before any Serve work begins, whether this
// process legitimately owns the UDS path. A late-bound daemon that
// loses the flock race must not unlink the incumbent peer's file.
func maybeStartStatusServer(
	ctx context.Context,
	cfg *Config,
	log *slog.Logger,
	src *statusExporter,
) (<-chan error, bool, error) {
	if cfg.StatusSocket == "" {
		return nil, false, nil
	}

	started := make(chan struct{})
	statusErrCh := make(chan error, 1)
	fn := currentServeStatusFn()

	go func() {
		err := fn(ctx, cfg.StatusSocket, cfg.StatusSocketGroup, src, started)
		if err != nil {
			log.Error("status server exited",
				slog.String("path", cfg.StatusSocket), slog.Any("err", err))
		}

		statusErrCh <- err
	}()

	select {
	case <-started:
		return statusErrCh, true, nil
	case err := <-statusErrCh:
		// The goroutine exited before reporting ready. Any bind-phase
		// failure — errAlreadyRunning most importantly — lands here.
		// Forward a sentinel-preserving copy so the caller can branch
		// on errors.Is(err, errAlreadyRunning) if it needs to emit a
		// dedicated exit code; otherwise the caller returns err and
		// runDaemon's own exit-code mapping handles it.
		return nil, false, err
	case <-time.After(statusReadyTimeout):
		log.Warn("status server did not signal ready within budget",
			slog.String("path", cfg.StatusSocket),
			slog.Duration("budget", statusReadyTimeout))

		// A timeout means the goroutine has not proven a successful
		// bind. Granting ownership on guess would register an unlink
		// defer that wipes the incumbent peer's socket if the bind
		// later fails. The caller still gets the error channel so
		// shutdown can drain the goroutine; bound stays false.
		return statusErrCh, false, nil
	}
}

// completeShutdown waits for the status server goroutine (if any) to
// exit and then reports why Serve returned. The exporter drain itself
// is handled by the deferred drainExporter call in runDaemon so the
// metrics HTTP server is guaranteed to stop BEFORE exporter drain:
// completeShutdown runs inside runDaemon's body, the defers run
// after completeShutdown returns. Serve errors that are merely ctx
// cancellation or closed-listener signals are suppressed; operators see
// the real cause via context.Cause instead of a noisy "use of closed
// connection" wrapper.
func completeShutdown(
	parentCtx, serveCtx context.Context,
	cfg *Config,
	log *slog.Logger,
	serveErr error,
	statusErrCh <-chan error,
) error {
	if statusErrCh != nil {
		select {
		case <-statusErrCh:
		case <-time.After(cfg.ShutdownTimeout):
			log.Warn("status server did not exit within shutdown budget")
		}
	}

	logShutdownCause(parentCtx, serveCtx, log)

	if serveErr != nil && !isExpectedServeExit(serveErr) {
		return fmt.Errorf("serve: %w", serveErr)
	}

	return nil
}

// logShutdownCause emits one structured log line naming why the Serve
// loop exited. Drain, SIGTERM, and generic cancellation each produce a
// distinct cause so operators can trace shutdown paths from logs.
func logShutdownCause(parentCtx, serveCtx context.Context, log *slog.Logger) {
	cause := context.Cause(serveCtx)
	if cause == nil {
		cause = context.Cause(parentCtx)
	}

	if cause == nil || errors.Is(cause, context.Canceled) {
		log.Info("usbipd shutdown complete")

		return
	}

	log.Info("usbipd shutdown complete", slog.String("cause", cause.Error()))
}

// isExpectedServeExit reports whether the Serve return value is a
// graceful-termination signal (ctx cancel, closed listener) that the
// caller should not surface as a runDaemon error.
func isExpectedServeExit(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed)
}

// systemdActivated reports whether the accept-path listener is about to
// come from systemd socket activation. The probe mirrors
// coreos/go-systemd's internal check: LISTEN_PID must equal our PID
// and LISTEN_FDS must be a positive integer. The answer is an input to
// status.listening.activation only — runDaemon still defers to
// listenOrActivation for the actual fd handoff.
func systemdActivated() bool {
	pid := os.Getenv("LISTEN_PID")
	fds := os.Getenv("LISTEN_FDS")

	if pid == "" || fds == "" {
		return false
	}

	if pid != strconv.Itoa(os.Getpid()) {
		return false
	}

	n, err := strconv.Atoi(fds)

	return err == nil && n > 0
}
