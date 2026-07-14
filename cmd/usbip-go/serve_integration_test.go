// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// runDaemonTestMu serialises tests that invoke runDaemon across this
// file. Parallelism is still permitted by paralleltest (every test
// calls t.Parallel) but the runDaemon-level invariants — package-
// scoped serveStatusFn swap, shared accept-path port pool — would
// race each other without this exclusion. Acquired at the top of
// each runDaemon-invoking test; released by t.Cleanup.
var runDaemonTestMu sync.Mutex

// lockRunDaemonForTest grabs runDaemonTestMu and schedules its release
// via t.Cleanup. Consolidates the lock idiom so every
// runDaemon-invoking test reads identically.
func lockRunDaemonForTest(t *testing.T) {
	t.Helper()
	runDaemonTestMu.Lock()
	t.Cleanup(runDaemonTestMu.Unlock)
}

// TestFinishDaemonShutdownKeepsStatusUntilExporterStops proves the status
// socket cannot be canceled (and therefore unlinked) while exporter Shutdown
// is still in flight. Channels provide exact ordering without sleeps.
func TestFinishDaemonShutdownKeepsStatusUntilExporterStops(t *testing.T) {
	t.Parallel()

	statusCtx, cancelStatus := context.WithCancel(t.Context())
	shutdownStarted := make(chan struct{})
	releaseShutdown := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		finishDaemonShutdown(func() {
			close(shutdownStarted)
			<-releaseShutdown
		}, cancelStatus)
	}()

	select {
	case <-shutdownStarted:
	case <-t.Context().Done():
		t.Fatal("exporter shutdown did not start")
	}

	select {
	case <-statusCtx.Done():
		t.Fatal("status context canceled before exporter shutdown completed")
	default:
	}

	close(releaseShutdown)

	select {
	case <-done:
	case <-t.Context().Done():
		t.Fatal("daemon shutdown coordinator did not return")
	}

	require.ErrorIs(t, statusCtx.Err(), context.Canceled)
}

// TestRunDaemonShutdownContract exercises the real runDaemon/status-UDS
// composition with a deterministic exporter. Each case proves Shutdown is
// called exactly once, the status endpoint remains responsive until that call
// returns, error/timeout outcomes are logged, and the UDS disappears only after
// runDaemon completes.
func TestRunDaemonShutdownContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		shutdownError error
		waitForExpiry bool
		wantWarning   string
	}{
		{
			name: "success",
		},
		{
			name:          "error",
			shutdownError: errTest,
			wantWarning:   "exporter shutdown returned error",
		},
		{
			name:          "timeout",
			waitForExpiry: true,
			wantWarning:   "context deadline exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lockRunDaemonForTest(t)

			var listenConfig net.ListenConfig

			listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })

			sockPath := filepath.Join(shortSocketTempDir(t), "status.sock")
			shutdownStarted := make(chan struct{})
			releaseShutdown := make(chan struct{})

			var shutdownCalls atomic.Int32

			fake := &fakeDaemonExporter{
				serve: func(ctx context.Context, _ net.Listener) error {
					<-ctx.Done()

					return ctx.Err()
				},
				shutdown: func(ctx context.Context) error {
					shutdownCalls.Add(1)
					close(shutdownStarted)

					if tt.waitForExpiry {
						<-ctx.Done()

						return ctx.Err()
					}

					<-releaseShutdown

					return tt.shutdownError
				},
			}

			deps := daemonDependencies{
				listen: func(context.Context, *ServeConfig) (net.Listener, error) {
					return listener, nil
				},
				buildExporter: func(*ServeConfig, *slog.Logger) (daemonExporter, error) {
					return fake, nil
				},
			}

			var logs bytes.Buffer

			log := slog.New(slog.NewTextHandler(&logs, nil))
			ctx := context.WithValue(t.Context(), loggerContextKey{}, log)

			cfg := &ServeConfig{
				Listen:           listener.Addr().String(),
				StatusSocket:     sockPath,
				ShutdownTimeout:  500 * time.Millisecond,
				HandshakeTimeout: time.Second,
				MaxSessions:      1,
			}

			done := make(chan error, 1)

			go func() {
				done <- runDaemonWithDependencies(ctx, cfg, deps)
			}()

			require.Eventually(t, func() bool {
				_, statErr := os.Stat(sockPath)

				return statErr == nil
			}, 2*time.Second, 20*time.Millisecond, "status socket not bound")

			client := newUDSHTTPClient(sockPath)
			t.Cleanup(client.CloseIdleConnections)

			req, err := http.NewRequestWithContext(
				t.Context(),
				http.MethodPost,
				"http://usbipd/drain",
				nil,
			)
			require.NoError(t, err)

			resp, err := client.Do(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusAccepted, resp.StatusCode)
			require.NoError(t, resp.Body.Close())

			select {
			case <-shutdownStarted:
			case <-time.After(2 * time.Second):
				t.Fatal("exporter shutdown did not start")
			}

			statusReq, err := http.NewRequestWithContext(
				t.Context(),
				http.MethodGet,
				"http://usbipd/",
				nil,
			)
			require.NoError(t, err)

			statusResp, err := client.Do(statusReq)
			require.NoError(t, err, "status UDS must remain responsive during exporter shutdown")
			require.Equal(t, http.StatusOK, statusResp.StatusCode)
			require.NoError(t, statusResp.Body.Close())

			if !tt.waitForExpiry {
				close(releaseShutdown)
			}

			select {
			case runErr := <-done:
				require.NoError(t, runErr)
			case <-time.After(3 * time.Second):
				t.Fatal("runDaemon did not finish after exporter shutdown")
			}

			require.Equal(t, int32(1), shutdownCalls.Load())

			_, statErr := os.Stat(sockPath)
			require.True(t, os.IsNotExist(statErr),
				"status socket must disappear after shutdown, stat err=%v", statErr)

			if tt.wantWarning == "" {
				require.NotContains(t, logs.String(), "exporter shutdown returned error")
			} else {
				require.Contains(t, logs.String(), tt.wantWarning)
			}
		})
	}
}

type fakeDaemonExporter struct {
	serve    func(context.Context, net.Listener) error
	shutdown func(context.Context) error
}

func (f *fakeDaemonExporter) Serve(ctx context.Context, listener net.Listener) error {
	return f.serve(ctx, listener)
}

func (f *fakeDaemonExporter) Shutdown(ctx context.Context) error {
	return f.shutdown(ctx)
}

func (f *fakeDaemonExporter) ListExported(context.Context) ([]usbip.Device, error) {
	return nil, nil
}

func (f *fakeDaemonExporter) Sessions(context.Context) []usbip.Session {
	return nil
}

// TestRunContextDrainExits spins the full run() composition (listener +
// exporter + status server + signal plumbing) and verifies POST /drain
// returns the process within ShutdownTimeout while context.Cause
// reports the drain trigger. The test exercises 8.4 wiring end-to-end
// without requiring a real kernel (pkg/usbip's non-linux stubs return
// ErrKernelModuleMissing during Exporter construction, so the test is
// gated on linux via the file-level build tag below).
func TestRunContextDrainExits(t *testing.T) {
	t.Parallel()
	lockRunDaemonForTest(t)

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	// Free port — bind & release so the Listen call inside run() picks
	// a port we already know is usable. Binding 127.0.0.1:0 returns a
	// concrete port; Close releases it so run()'s net.Listen can rebind.
	var lc net.ListenConfig

	probe, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := probe.Addr().String()
	require.NoError(t, probe.Close())

	cfg := &ServeConfig{
		Listen:            addr,
		StatusSocket:      sockPath,
		StatusSocketGroup: "",
		MaxSessions:       16,
		HandshakeTimeout:  2 * time.Second,
		ShutdownTimeout:   5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)

	go func() {
		done <- runDaemon(ctx, cfg)
	}()

	// Wait for status socket to appear so the HTTP client has a target.
	require.Eventually(t, func() bool {
		_, statErr := os.Stat(sockPath)

		return statErr == nil
	}, 3*time.Second, 50*time.Millisecond, "status socket not bound")

	// POST /drain triggers drain; the run goroutine should exit cleanly
	// within the shutdown window.
	client := &http.Client{
		Transport: &http.Transport{
			DialContext: func(dctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer

				return d.DialContext(dctx, "unix", sockPath)
			},
		},
		Timeout: 3 * time.Second,
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://usbipd/drain", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusAccepted, resp.StatusCode,
		"first POST /drain returns 202 Accepted (RFC 9110 §15.3.3)")

	select {
	case runErr := <-done:
		// Graceful drain resolves with the exporter's shutdown returning
		// nil (no outstanding sessions on this test host).
		require.NoError(t, runErr, "runDaemon returned non-nil after drain")
	case <-time.After(8 * time.Second):
		t.Fatal("runDaemon did not exit within 8 seconds of POST /drain")
	}

	// runDaemon's cleanup path must unlink the status socket
	// unconditionally. A status-goroutine defer silently no-ops if the
	// goroutine exits via completeShutdown's force-timeout branch.
	_, statErr := os.Stat(sockPath)
	require.True(t, os.IsNotExist(statErr),
		"status socket must be unlinked after runDaemon returns, stat err=%v", statErr)
}

// TestRunDaemonUnlinksStatusSocketOnForcedShutdown pins the
// forced-shutdown cleanup invariant: when the status goroutine never
// gets a chance to run its deferred cleanup (e.g. completeShutdown
// times out and the process is killed with os.Exit), run()'s own
// cleanup path MUST still unlink the status socket. The test swaps
// serveStatusFn for a stub that binds the UDS like real life, signals
// ready, then blocks on ctx.Done() WITHOUT closing the listener or
// removing the file — emulating an os.Exit that skips goroutine
// defers.
func TestRunDaemonUnlinksStatusSocketOnForcedShutdown(t *testing.T) {
	t.Parallel()
	lockRunDaemonForTest(t)

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	// Install a stub that binds + announces ready but deliberately
	// SKIPS BOTH listener.Close AND os.Remove, simulating an os.Exit
	// that killed the goroutine before its defer ran. Go's
	// net.UnixListener.Close unlinks the socket file on success, so
	// skipping Close is required to exercise the forced-shutdown
	// cleanup path: without runDaemon's own cleanup the file leaks.
	//
	// The stub holds the listener in the test-scoped leakedLis var so
	// the OS-level fd is reaped by t.Cleanup; the UDS file is the
	// surface under test and must come from runDaemon's defer.
	var leakedLis atomic.Value

	originalFn := swapServeStatusFn(func(
		sctx context.Context, path, _ string,
		_ statusSource, started chan<- struct{},
	) error {
		var lc net.ListenConfig

		lis, lerr := lc.Listen(sctx, "unix", path)
		if lerr != nil {
			return fmt.Errorf("test stub listen: %w", lerr)
		}

		leakedLis.Store(lis)

		if started != nil {
			close(started)
		}

		<-sctx.Done()

		// Intentionally DO NOT close lis or unlink path. runDaemon's
		// own defer MUST unlink the file.
		return nil
	})

	t.Cleanup(func() {
		swapServeStatusFn(originalFn)

		lv := leakedLis.Load()
		if lv != nil {
			lis, _ := lv.(net.Listener)
			if lis != nil {
				_ = lis.Close()
			}
		}
	})

	var lc net.ListenConfig

	probe, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := probe.Addr().String()
	require.NoError(t, probe.Close())

	cfg := &ServeConfig{
		Listen:            addr,
		StatusSocket:      sockPath,
		StatusSocketGroup: "",
		MaxSessions:       16,
		HandshakeTimeout:  2 * time.Second,
		ShutdownTimeout:   2 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)

	go func() {
		done <- runDaemon(ctx, cfg)
	}()

	require.Eventually(t, func() bool {
		_, statErr := os.Stat(sockPath)

		return statErr == nil
	}, 3*time.Second, 50*time.Millisecond, "status socket not bound")

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of context cancel")
	}

	_, statErr := os.Stat(sockPath)
	require.True(t, os.IsNotExist(statErr),
		"status socket must be unlinked after forced shutdown, stat err=%v", statErr)
}

// swapServeStatusFn atomically replaces the package-level serveStatusFn
// hook and returns the previous value. The test uses it to install a
// stub that emulates a status goroutine whose defer never ran.
func swapServeStatusFn(fn func(
	ctx context.Context, path, group string,
	src statusSource, started chan<- struct{},
) error,
) func(ctx context.Context, path, group string, src statusSource, started chan<- struct{}) error {
	serveStatusFnMu.Lock()
	defer serveStatusFnMu.Unlock()

	prev := serveStatusFn

	serveStatusFn = fn

	return prev
}
