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

	// Owned cleanup for the status UDS file (Finding 4). Owned HERE
	// rather than in the status goroutine's defer because that defer
	// is skipped when completeShutdown times out with the goroutine
	// still serving; if this function returns, the socket file MUST
	// be unlinked regardless of goroutine state.
	if cfg.StatusSocket != "" {
		defer func() { _ = os.Remove(cfg.StatusSocket) }()
	}

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

	// Two-stage LIFO shutdown (Finding 6): the metrics HTTP server must
	// go down BEFORE exporter drain so /readyz and /metrics cannot hand
	// out stale "accepting=true" or fresh session counters while the
	// exporter is winding down. LIFO means the LAST-registered defer
	// runs FIRST, so we register exp-drain first, then metrics-stop.
	defer drainExporter(ctx, cfg, exp, log)

	if metricsStop != nil {
		defer shutdownMetricsServer(ctx, metricsStop, cfg.ShutdownTimeout, log)
	}

	src.setDrain(func() {
		cancelServe(errDrainRequested)
	})

	statusErrCh := maybeStartStatusServer(statusCtx, cfg, log, src)

	log.Info("usbipd accepting connections",
		slog.String("addr", listener.Addr().String()),
		slog.Bool("activation", activated))

	src.markAccepting(true)

	serveErr := exp.Serve(serveCtx, listener)

	src.markAccepting(false)

	// Wind down the status UDS after Serve returns. The server is kept
	// alive through Serve so `usbipd drain` can poll sessions=[] during
	// the drain window; cancelling it here lets completeShutdown
	// observe statusErrCh without racing the drain.
	cancelStatus()

	return completeShutdown(ctx, serveCtx, cfg, log, serveErr, statusErrCh)
}

// drainExporter is the deferred Exporter.Shutdown callpoint. Extracted
// from completeShutdown (Finding 6) so the deferred-stack ordering —
// metrics HTTP server down FIRST, exporter drain SECOND — is visible in
// runDaemon without hiding the ordering inside completeShutdown's
// branch-heavy body.
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
// gauge during construction. This replaces the pre-Finding 7 pattern of
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
	// WithExporterBuildInfo (Finding 7); the metrics-HTTP server just
	// serves promhttp.HandlerFor(reg) on the already-populated
	// registry. A second MustNewMetrics call here would re-register
	// the §11.5.5 collectors and panic on duplicate registration.

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
// reads the accepting flag from the exporter, and checks the status
// socket file for writability (when configured).
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
// counts as "writable" because there is nothing to gate on. A non-empty
// path must actually exist on the filesystem — missing file means the
// UDS server failed to bind or was unlinked.
func statusSocketWritable(path string) bool {
	if path == "" {
		return true
	}

	_, err := os.Stat(path)

	return err == nil
}

// serveStatusFn is the indirection through which runDaemon launches
// the status UDS server. Production wiring is serveStatus itself; unit
// tests override this variable to inject a fake that deliberately
// SKIPS the listener/unlink cleanup so the Finding 4 invariant ("run()
// owns the unlink") can be RED-tested. Protected by serveStatusFnMu
// so parallel tests replacing the hook are race-free.
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
// cfg.StatusSocket is non-empty. Returns a receive-only error channel
// the caller monitors during shutdown; nil when the endpoint is
// disabled.
func maybeStartStatusServer(
	ctx context.Context,
	cfg *Config,
	log *slog.Logger,
	src *statusExporter,
) <-chan error {
	if cfg.StatusSocket == "" {
		return nil
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
	case <-time.After(statusReadyTimeout):
		log.Warn("status server did not signal ready within budget",
			slog.String("path", cfg.StatusSocket),
			slog.Duration("budget", statusReadyTimeout))
	}

	return statusErrCh
}

// completeShutdown waits for the status server goroutine (if any) to
// exit and then reports why Serve returned. The exporter drain itself
// is handled by the deferred drainExporter call in runDaemon (Finding
// 6) so the metrics HTTP server is guaranteed to stop BEFORE exporter
// drain: completeShutdown runs inside runDaemon's body, the defers run
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
