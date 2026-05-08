package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsReadHeaderTimeout bounds HTTP header reads on the metrics
// server. Prometheus scrapes are short by design — 5 seconds is ample
// while cutting off slow-loris style misuse (gosec G112).
const metricsReadHeaderTimeout = 5 * time.Second

// readinessRefreshInterval caps how often newReadinessChecker invokes
// the underlying probe. Five seconds matches the §11.5.5 "poll every
// 5s" contract so a /readyz flood cannot hammer /sys/module.
const readinessRefreshInterval = 5 * time.Second

// readinessState is the snapshot the /readyz handler consults. Every
// field is an input to the 503/200 decision: all modules Loaded AND
// ListenerBound AND Accepting AND StatusWritable → 200, otherwise 503.
//
// ListenerBound and Accepting are split so /readyz cannot report 200
// during the gap between "exporter configured" and "accept loop
// actually processing connections": ListenerBound flips true once the
// bind confirms a non-nil Addr, Accepting flips true only after the
// accept loop has produced its first successful net.Listener.Accept
// return. Collapsing the two flags into Accepting (set BEFORE Serve
// began) would let a kernel bind failure landing mid-startup leave
// /readyz green while the TCP surface was unreachable.
type readinessState struct {
	ListenerBound  bool
	Accepting      bool
	StatusWritable bool
	Modules        map[string]usbip.ModuleState
}

// readinessProbe is the closure signature consumed by
// newReadinessChecker. Exposed as a named type so tests can drop a
// fake probe in without building a full statusExporter.
type readinessProbe func(ctx context.Context) readinessState

// readinessChecker caches the readinessProbe output for up to
// readinessRefreshInterval so a /readyz flood cannot back-pressure the
// underlying kernel-module probe. Safe for concurrent use: probe
// invocation happens inside cacheMu so the flush is exactly-once per
// TTL boundary.
type readinessChecker struct {
	probe readinessProbe

	cacheMu     sync.Mutex
	cachedState readinessState
	cacheExpiry time.Time
}

// newReadinessChecker wraps probe in the readinessRefreshInterval cache.
func newReadinessChecker(probe readinessProbe) *readinessChecker {
	return &readinessChecker{probe: probe}
}

// state returns the cached readinessState, refreshing it on TTL expiry.
func (c *readinessChecker) state(ctx context.Context) readinessState {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	now := time.Now()
	if !c.cacheExpiry.IsZero() && now.Before(c.cacheExpiry) {
		return c.cachedState
	}

	c.cachedState = c.probe(ctx)
	c.cacheExpiry = now.Add(readinessRefreshInterval)

	return c.cachedState
}

// ready reports whether the current state satisfies the §11.5.5
// readiness contract: every required module Loaded, the listener
// bound AND the accept loop actually accepting, and the status socket
// writable (or disabled). All four inputs must be true — a false on
// any short-circuits the reply to 503.
func (s readinessState) ready() bool {
	if !s.ListenerBound || !s.Accepting || !s.StatusWritable {
		return false
	}

	for _, required := range []string{"usbip_core", "vhci_hcd", "usbip_host"} {
		if s.Modules[required] != usbip.ModuleStateLoaded {
			return false
		}
	}

	return true
}

// newMetricsMux wires /metrics, /healthz, /readyz on a fresh ServeMux.
// The mux is kept minimal so other daemon HTTP surfaces (status UDS,
// future debug endpoints) stay decoupled.
func newMetricsMux(reg *prometheus.Registry, checker *readinessChecker) *http.ServeMux {
	mux := http.NewServeMux()

	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	}))

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writePlain(w, http.StatusOK, "ok")
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		st := checker.state(r.Context())
		if !st.ready() {
			writePlain(w, http.StatusServiceUnavailable, "not ready")

			return
		}

		writePlain(w, http.StatusOK, "ok")
	})

	return mux
}

// writePlain writes a plaintext body and status code, swallowing write
// errors because the client has already disconnected if Write fails.
func writePlain(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)

	_, _ = io.WriteString(w, body+"\n")
}

// metricsServer owns the HTTP listener + server backing --metrics-addr.
// Shutdown is idempotent via once; Addr is stored after Listen so
// tests hitting 127.0.0.1:0 can discover the concrete bind.
type metricsServer struct {
	server     *http.Server
	listener   net.Listener
	addr       string
	serveErrCh chan error

	shutdownOnce sync.Once
	shutdownErr  atomic.Pointer[error]
}

// startMetricsServer is a convenience wrapper around
// startMetricsServerWithHandle for callers that only need the shutdown
// func. Returns (nil, nil) when addr is empty — the caller treats "no
// metrics" identically to "successful no-op startup".
func startMetricsServer(
	ctx context.Context,
	addr string,
	reg *prometheus.Registry,
	checker *readinessChecker,
) (func(context.Context) error, error) {
	ms, err := startMetricsServerWithHandle(ctx, addr, reg, checker)
	if err != nil {
		return nil, err
	}

	if ms == nil {
		return nil, nil //nolint:nilnil // matches startMetricsServerWithHandle's documented nop signal
	}

	return ms.shutdown, nil
}

// runServe is the Serve-on-listener goroutine entry point.
func (m *metricsServer) runServe() {
	err := m.server.Serve(m.listener)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		m.serveErrCh <- err
	}

	close(m.serveErrCh)
}

// shutdown drains the HTTP server bounded by ctx. Idempotent: a second
// call returns the same error the first one observed. Reports Serve
// errors (non-ErrServerClosed) that surfaced before Shutdown was called.
func (m *metricsServer) shutdown(ctx context.Context) error {
	m.shutdownOnce.Do(func() {
		err := m.server.Shutdown(ctx)
		if err != nil {
			wrapped := fmt.Errorf("metrics shutdown: %w", err)
			m.shutdownErr.Store(&wrapped)

			return
		}

		// Surface any serve error that fired before shutdown.
		for serveErr := range m.serveErrCh {
			if serveErr == nil {
				continue
			}

			wrapped := fmt.Errorf("metrics serve: %w", serveErr)
			m.shutdownErr.Store(&wrapped)
		}
	})

	ptr := m.shutdownErr.Load()
	if ptr == nil {
		return nil
	}

	return *ptr
}

// startMetricsServerWithHandle is the richer constructor tests use to
// get both the stop func and the concrete addr. Production code can
// call this too; startMetricsServer is a thin wrapper that drops the
// handle for callers that only need the stop func.
func startMetricsServerWithHandle(
	ctx context.Context,
	addr string,
	reg *prometheus.Registry,
	checker *readinessChecker,
) (*metricsServer, error) {
	if addr == "" {
		return nil, nil //nolint:nilnil // matches startMetricsServer's "addr empty" contract
	}

	var lc net.ListenConfig

	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("metrics listen %q: %w", addr, err)
	}

	ms := &metricsServer{
		listener:   lis,
		addr:       lis.Addr().String(),
		serveErrCh: make(chan error, 1),
	}

	ms.server = &http.Server{
		Handler:           newMetricsMux(reg, checker),
		ReadHeaderTimeout: metricsReadHeaderTimeout,
	}

	go ms.runServe()

	return ms, nil
}
