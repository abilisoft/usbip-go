package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
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

	exp, err := buildExporter(cfg, log)
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

	return completeShutdown(ctx, serveCtx, cfg, exp, log, serveErr, statusErrCh)
}

// buildExporter constructs the pkg/usbip.Exporter from cfg. The
// translation of Config fields to With* options lives here so runDaemon
// stays focused on lifecycle rather than option plumbing.
func buildExporter(cfg *Config, log *slog.Logger) (*usbip.Exporter, error) {
	opts := []usbip.ExporterOption{
		usbip.WithExporterLogger(log),
		usbip.WithExporterMaxSessions(cfg.MaxSessions),
		usbip.WithExporterMaxSessionsPerPeer(cfg.MaxSessionsPerPeer),
		usbip.WithExporterAcceptRateLimit(cfg.AcceptRateLimit),
		usbip.WithExporterMaxHandshakeBytes(cfg.MaxHandshakeBytes),
		usbip.WithExporterHandshakeTimeout(cfg.HandshakeTimeout),
		usbip.WithExporterShutdownTimeout(cfg.ShutdownTimeout),
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

// completeShutdown drains the exporter under cfg.ShutdownTimeout and
// then waits for the status server goroutine (if any) to exit. Serve
// errors that are merely ctx cancellation or closed-listener signals
// are suppressed; operators see the real cause via context.Cause
// instead of a noisy "use of closed connection" wrapper.
func completeShutdown(
	parentCtx, serveCtx context.Context,
	cfg *Config,
	exp *usbip.Exporter,
	log *slog.Logger,
	serveErr error,
	statusErrCh <-chan error,
) error {
	// shutdownCtx must outlive the cancelled parent so Shutdown can
	// actually drain; context.WithoutCancel detaches the cancellation
	// while keeping values and deadlines we care about.
	shutdownCtx, cancel := context.WithTimeout(
		context.WithoutCancel(parentCtx), cfg.ShutdownTimeout)
	defer cancel()

	drainErr := exp.Shutdown(shutdownCtx)
	if drainErr != nil {
		log.Warn("exporter shutdown returned error",
			slog.Any("err", drainErr))
	}

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
