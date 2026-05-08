package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"

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

	mu       sync.RWMutex
	shutdown bool
	serving  bool

	wg sync.WaitGroup
}

// NewExporter constructs an Exporter from functional options. Required
// dependencies missing from opts cause a panic because a missing
// dependency is a programming error, not a runtime condition worth
// propagating up the call stack.
func NewExporter(opts ...ExporterOption) *Exporter {
	cfg := exporterConfig{clock: RealClock{}, logger: slog.Default()}

	for _, opt := range opts {
		opt(&cfg)
	}

	requireExporterDeps(&cfg)

	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}

	return &Exporter{
		kernel:    cfg.kernel,
		events:    cfg.events,
		transport: cfg.transport,
		codec:     cfg.codec,
		clock:     cfg.clock,
		logger:    cfg.logger,
	}
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
		return fmt.Errorf("bind %s: %w", busID, err)
	}

	return nil
}

// Unbind delegates to kernel.Unbind per spec §5.3. Errors are wrapped
// with the busid.
func (e *Exporter) Unbind(ctx context.Context, busID domain.BusID) error {
	err := e.kernel.Unbind(ctx, busID)
	if err != nil {
		return fmt.Errorf("unbind %s: %w", busID, err)
	}

	return nil
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
	err := e.startServing()
	if err != nil {
		return err
	}

	defer e.stopServing()

	stopWatcher := e.spawnCtxListenerCloser(ctx, listener)
	defer stopWatcher()

	loopErr := e.acceptLoop(ctx, listener)

	// Wait for every per-conn goroutine we spawned to drain. Without
	// this, Serve can return while session handlers are mid-handshake;
	// goleak would (rightly) flag the leaked goroutines in TestMain.
	e.wg.Wait()

	return loopErr
}

// Shutdown stops accepting new connections and signals in-flight
// sessions to drain. Task 5.10 lands the minimal "mark shutdown + reject
// future Serve" semantics required by the accept-loop RED tests;
// Task 5.12 extends this with the bounded drain wait over in-flight
// session handles. Idempotent: a second Shutdown returns nil.
func (e *Exporter) Shutdown(_ context.Context) error {
	e.mu.Lock()

	if e.shutdown {
		e.mu.Unlock()

		return nil
	}

	e.shutdown = true

	e.mu.Unlock()

	return nil
}

// startServing transitions the Exporter from idle → serving. Returns
// ErrAlreadyShutdown when Shutdown has run or ErrServeAlreadyRunning
// when Serve is already running (overlapping Serve calls are
// unsupported).
func (e *Exporter) startServing() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.shutdown {
		return ErrAlreadyShutdown
	}

	if e.serving {
		return ErrServeAlreadyRunning
	}

	e.serving = true

	return nil
}

// stopServing flips the serving flag back to false so a caller that
// wants to re-Serve after a non-shutdown Serve exit can do so.
func (e *Exporter) stopServing() {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.serving = false
}

// spawnCtxListenerCloser watches ctx and closes listener exactly once
// when ctx is cancelled. Returns a stop func so Serve can cancel the
// watcher cleanly on a normal return — without it, the watcher would
// leak a goroutine waiting on ctx forever.
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

	return func() { close(stop) }
}

// acceptLoop pulls connections off listener and dispatches each to a
// fresh handler goroutine. Accept errors that indicate a closed
// listener (either ctx-driven close or explicit Close) terminate the
// loop via the shared acceptShouldStop helper; other errors are
// surfaced to the caller wrapped with context.
func (e *Exporter) acceptLoop(ctx context.Context, listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if acceptShouldStop(ctx, err) {
				return nil
			}

			return fmt.Errorf("exporter accept: %w", err)
		}

		e.wg.Go(func() {
			e.handleConn(ctx, conn)
		})
	}
}

// acceptShouldStop reports whether the accept error is a normal stop
// signal (ctx-driven or listener closed) vs a fatal error that Serve
// must surface to the caller. Factored out to isolate the //nilerr
// pattern behind a named predicate.
func acceptShouldStop(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return true
	}

	return errors.Is(err, net.ErrClosed)
}
