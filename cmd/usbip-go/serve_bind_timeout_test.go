// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestRunDaemonDoesNotClaimOwnershipOnBindTimeout pins the invariant
// that a status goroutine which never signals ready within the
// statusReadyTimeout budget must not let runDaemon register the
// unlink defer. The goroutine might eventually fail to bind, and a
// runDaemon that already claimed ownership would wipe the incumbent
// peer's socket on its way out. The ownership flag is granted only
// by a concrete `started` signal from the goroutine, never by a
// timer.
func TestRunDaemonDoesNotClaimOwnershipOnBindTimeout(t *testing.T) {
	t.Parallel()
	lockRunDaemonForTest(t)

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	err := os.WriteFile(sockPath, []byte("incumbent"), 0o600)
	require.NoError(t, err)

	// Stub that blocks on ctx.Done() without ever closing `started`
	// and without binding. Forces maybeStartStatusServer through the
	// timeout branch.
	originalFn := swapServeStatusFn(func(
		sctx context.Context, _, _ string,
		_ statusSource, _ chan<- struct{},
	) error {
		<-sctx.Done()

		return nil
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
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	done := make(chan error, 1)

	go func() {
		done <- runDaemon(ctx, cfg)
	}()

	// statusReadyTimeout is 3 s; wait long enough for the timeout
	// branch to fire, then cancel so runDaemon exits.
	time.Sleep(4 * time.Second)

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runDaemon did not return within 5s of context cancel")
	}

	content, statErr := os.ReadFile(filepath.Clean(sockPath))
	require.NoError(t, statErr,
		"incumbent status socket was unlinked after a bind-timeout branch")
	require.Equal(t, "incumbent", string(content),
		"incumbent socket content was overwritten")
}
