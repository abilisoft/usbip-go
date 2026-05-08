// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// healthRequestTimeout bounds a /healthz or /readyz handler's
// processing time. Probes that hang past this budget fail closed:
// the handler returns 503 so liveness/readiness controllers do not
// hang on a stuck readiness probe.
const healthRequestTimeout = 2 * time.Second

// healthReadHeaderTimeout caps how long the health server will wait
// for an inbound request's headers. Defends the daemon from a slow-
// loris probe holding the listener socket open.
const healthReadHeaderTimeout = 5 * time.Second

// readinessProbe is the closure invoked by the /readyz handler on
// every request. It returns a snapshot of the daemon's readiness
// inputs (kernel modules, listener bound, accept loop running, status
// socket writable). Implementations must be safe for concurrent
// invocation.
type readinessProbe func(ctx context.Context) readinessState

// readinessState carries the four signals consumed by ready(). Zero
// value (no modules loaded, every flag false) reports "not ready" so
// a probe that returns the zero value never accidentally green-lights
// traffic.
type readinessState struct {
	ListenerBound  bool
	Accepting      bool
	StatusWritable bool
	Modules        map[string]usbip.ModuleState
}

// requiredKernelModules is the closed set of modules that MUST be
// loaded for the exporter to function. Every name in this slice must
// appear with usbip.ModuleStateLoaded in readinessState.Modules
// before /readyz returns 200.
var requiredKernelModules = []string{"usbip_core", "usbip_host"}

// ready reports whether every readiness input is satisfied. Returns
// false when any required kernel module is missing, the listener is
// not bound, the accept loop is not running, or the status socket is
// not writable. Used by the /readyz handler to map state → HTTP code.
func (s readinessState) ready() bool {
	if !s.ListenerBound || !s.Accepting || !s.StatusWritable {
		return false
	}

	for _, name := range requiredKernelModules {
		if s.Modules[name] != usbip.ModuleStateLoaded {
			return false
		}
	}

	return true
}

// newReadinessChecker returns an http.Handler that runs probe on each
// request and writes 200 OK when ready() is true, 503 otherwise. The
// handler applies a per-request timeout so a wedged probe cannot stall
// the health server's accept loop.
func newReadinessChecker(probe readinessProbe) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), healthRequestTimeout)
		defer cancel()

		state := probe(ctx)
		if !state.ready() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)

			return
		}

		w.WriteHeader(http.StatusOK)
	})
}

// newLivenessChecker returns a constant 200 OK handler. Liveness here
// means "the process is up and the HTTP server is serving"; deeper
// checks belong on /readyz so a transiently-unhealthy daemon does not
// get killed and restart-flapped.
func newLivenessChecker() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// startHealthServer binds an HTTP listener on addr and serves /healthz
// + /readyz. Returns a shutdown func the caller must invoke to drain
// in-flight requests and close the listener. The shutdown func runs
// http.Server.Shutdown under its own context so a wedged handler
// cannot hang daemon exit.
//
// The Serve goroutine writes its terminal error onto a buffered
// channel; the stop callback synchronizes on that channel rather
// than reading a shared variable, so go test -race stays clean.
func startHealthServer(
	ctx context.Context,
	addr string,
	probe readinessProbe,
) (func(context.Context) error, error) {
	mux := http.NewServeMux()
	mux.Handle("/healthz", newLivenessChecker())
	mux.Handle("/readyz", newReadinessChecker(probe))

	lc := &net.ListenConfig{}

	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind health listener %q: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: healthReadHeaderTimeout,
	}

	// Buffered so the Serve goroutine can publish its terminal error
	// without blocking on a stop callback that may never read.
	serveDone := make(chan error, 1)

	go func() {
		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}

		serveDone <- err
	}()

	stop := func(stopCtx context.Context) error {
		shutdownErr := srv.Shutdown(stopCtx)

		// Wait for Serve to return — but only up to the same stopCtx
		// deadline. A wedged handler that ignores Shutdown's drain
		// would otherwise block daemon exit forever (Shutdown returns
		// the deadline error, but its Serve goroutine never finishes
		// because the in-flight handler refuses to release).
		var serveErr error
		select {
		case serveErr = <-serveDone:
		case <-stopCtx.Done():
			serveErr = stopCtx.Err()
		}

		if shutdownErr != nil {
			return fmt.Errorf("shutdown health server: %w", shutdownErr)
		}

		return serveErr
	}

	return stop, nil
}

// shutdownHealthServer performs the deferred HTTP shutdown under a
// timeout derived from parentCtx so a wedged handler cannot hang
// daemon exit. Errors are logged at warn level.
func shutdownHealthServer(
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
		log.Warn("health server shutdown returned error", slog.Any("err", err))
	}
}
