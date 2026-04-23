//go:build linux

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	// Free port — bind & release so the Listen call inside run() picks
	// a port we already know is usable. Binding 127.0.0.1:0 returns a
	// concrete port; Close releases it so run()'s net.Listen can rebind.
	var lc net.ListenConfig

	probe, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	addr := probe.Addr().String()
	require.NoError(t, probe.Close())

	cfg := &Config{
		Listen:            addr,
		StatusSocket:      sockPath,
		StatusSocketGroup: "",
		MaxSessions:       16,
		HandshakeTimeout:  2 * time.Second,
		ShutdownTimeout:   5 * time.Second,
		LogLevel:          "error",
		LogFormat:         "json",
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
	require.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case runErr := <-done:
		// Graceful drain resolves with the exporter's shutdown returning
		// nil (no outstanding sessions on this test host).
		require.NoError(t, runErr, "runDaemon returned non-nil after drain")
	case <-time.After(8 * time.Second):
		t.Fatal("runDaemon did not exit within 8 seconds of POST /drain")
	}

	// Phase 8 review Finding 4: runDaemon's cleanup path must unlink
	// the status socket unconditionally. Previously this was the status
	// goroutine's defer, which silently no-ops if the goroutine exits
	// via completeShutdown's force-timeout branch.
	_, statErr := os.Stat(sockPath)
	require.True(t, os.IsNotExist(statErr),
		"status socket must be unlinked after runDaemon returns, stat err=%v", statErr)
}

// TestRunDaemonUnlinksStatusSocketOnForcedShutdown proves Finding 4's
// core invariant: when the status goroutine never gets a chance to
// run its deferred cleanup (e.g. completeShutdown times out and the
// process is killed with os.Exit), run()'s own cleanup path MUST
// still unlink the status socket. The test swaps serveStatusFn for a
// stub that binds the UDS like real life, signals ready, then blocks
// on ctx.Done() WITHOUT closing the listener or removing the file —
// emulating an os.Exit that skips goroutine defers.
func TestRunDaemonUnlinksStatusSocketOnForcedShutdown(t *testing.T) {
	t.Parallel()
	lockRunDaemonForTest(t)

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	// Install a stub that binds + announces ready but deliberately
	// SKIPS BOTH listener.Close AND os.Remove, simulating an os.Exit
	// that killed the goroutine before its defer ran. Go's
	// net.UnixListener.Close unlinks the socket file on success, so
	// skipping Close is required to expose the Finding 4 bug: without
	// runDaemon's own cleanup the file leaks.
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

	cfg := &Config{
		Listen:            addr,
		StatusSocket:      sockPath,
		StatusSocketGroup: "",
		MaxSessions:       16,
		HandshakeTimeout:  2 * time.Second,
		ShutdownTimeout:   2 * time.Second,
		LogLevel:          "error",
		LogFormat:         "json",
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
) error) func(ctx context.Context, path, group string, src statusSource, started chan<- struct{}) error {
	serveStatusFnMu.Lock()
	defer serveStatusFnMu.Unlock()

	prev := serveStatusFn

	serveStatusFn = fn

	return prev
}
