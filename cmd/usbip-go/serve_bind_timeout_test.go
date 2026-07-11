// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestMaybeStartStatusServerDoesNotClaimOwnershipOnBindTimeout pins the
// invariant that a status goroutine which never signals ready returns
// bound=false. A caller may register ownership-sensitive cleanup only after a
// concrete `started` signal, never after a timer expires.
func TestMaybeStartStatusServerDoesNotClaimOwnershipOnBindTimeout(t *testing.T) {
	t.Parallel()
	lockRunDaemonForTest(t)

	const testReadyTimeout = 20 * time.Millisecond

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	err := os.WriteFile(sockPath, []byte("incumbent"), 0o600)
	require.NoError(t, err)

	// Stub that blocks on ctx.Done() without ever closing `started` and without
	// binding. This forces maybeStartStatusServer through the timeout branch.
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

	cfg := &ServeConfig{
		StatusSocket:      sockPath,
		StatusSocketGroup: "",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	log := slog.New(slog.DiscardHandler)

	statusErrCh, bound, startErr := maybeStartStatusServer(
		ctx, cfg, log, nil, testReadyTimeout,
	)
	require.NoError(t, startErr)
	require.False(t, bound, "a timeout must not claim status-socket ownership")
	require.NotNil(t, statusErrCh)

	cancel()

	select {
	case statusErr := <-statusErrCh:
		require.NoError(t, statusErr)
	case <-time.After(time.Second):
		t.Fatal("status goroutine did not return after context cancel")
	}

	content, statErr := os.ReadFile(filepath.Clean(sockPath))
	require.NoError(t, statErr,
		"incumbent status socket was unlinked after the timeout branch")
	require.Equal(t, "incumbent", string(content),
		"incumbent socket content was overwritten")
}
