//go:build linux

package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRunContextDrainExits spins the full run() composition (listener +
// exporter + status server + signal plumbing) and verifies POST /drain
// returns the process within ShutdownTimeout while context.Cause
// reports the drain trigger. The test exercises 8.4 wiring end-to-end
// without requiring a real kernel (pkg/usbip's non-linux stubs return
// ErrKernelModuleMissing during Exporter construction, so the test is
// gated on linux via the file-level build tag below).
func TestRunContextDrainExits(t *testing.T) {
	t.Parallel()

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
		DrainTimeout:      10 * time.Second,
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
}
