// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRunDaemonPreservesStatusSocketOnBindCollision pins the invariant:
// when this daemon fails to bind the status UDS because another live
// instance already holds it, runDaemon must not unlink that live
// instance's socket file on its way out. The unconditional os.Remove
// defer registered before bind would otherwise wipe a running
// daemon's UDS, breaking every status consumer and the drain
// subcommand attached to the live instance.
//
// The stub replaces serveStatus with an immediate errAlreadyRunning
// return (without closing `started`), emulating the losing half of a
// flock race. A sentinel file staged on disk stands in for the
// incumbent daemon's socket; after runDaemon returns the sentinel
// must still exist.
func TestRunDaemonPreservesStatusSocketOnBindCollision(t *testing.T) {
	t.Parallel()
	lockRunDaemonForTest(t)

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	// Stage a placeholder representing the live instance's UDS. Any
	// unlink by the losing daemon wipes this file; the test asserts
	// the file survives.
	err := os.WriteFile(sockPath, []byte("incumbent"), 0o600)
	require.NoError(t, err)

	originalFn := swapServeStatusFn(func(
		_ context.Context, _, _ string,
		_ statusSource, _ chan<- struct{},
	) error {
		return fmt.Errorf("%w: %s", errAlreadyRunning, sockPath)
	})

	t.Cleanup(func() {
		swapServeStatusFn(originalFn)
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
		LogLevel:          "error",
		LogFormat:         "json",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)

	go func() {
		done <- runDaemon(ctx, cfg)
	}()

	// Give runDaemon time to observe the collision, then cancel so it
	// exits via ctx.Done rather than waiting on a real Serve loop.
	require.Eventually(t, func() bool {
		return statusErrAlreadyRunningObserved(t, sockPath)
	}, 3*time.Second, 50*time.Millisecond,
		"runDaemon did not observe the collision via statusErrCh")

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of context cancel")
	}

	// The incumbent's socket MUST still be on disk: the losing daemon
	// had no right to unlink it.
	content, statErr := os.ReadFile(filepath.Clean(sockPath))
	require.NoError(t, statErr,
		"incumbent status socket was unlinked by losing daemon: %v", statErr)
	require.Equal(t, "incumbent", string(content),
		"incumbent status socket content was overwritten")
}

// statusErrAlreadyRunningObserved reports whether the sockPath file's
// contents are still the sentinel — a proxy for "the losing daemon
// has not yet unlinked it". The invariant must hold at every sample
// point, so a false return here after startup means the defer has
// already fired and the test can fail fast.
func statusErrAlreadyRunningObserved(t *testing.T, sockPath string) bool {
	t.Helper()

	content, err := os.ReadFile(filepath.Clean(sockPath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			t.Fatalf("incumbent socket unlinked before ctx cancel: %v", err)
		}

		return false
	}

	return string(content) == "incumbent"
}
