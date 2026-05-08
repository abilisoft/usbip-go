// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMain handles BOTH the daemon-side TMPDIR redirect (AF_UNIX
// bind under sandboxed CI requires the socket path to live inside
// the project root) and the importer-side flag-completion-
// registration suppression (parallel root-construction tests would
// otherwise race cobra's global flagCompletionFunctions map).
func TestMain(m *testing.M) {
	skipFlagCompletionRegistration = true

	tmp, err := os.MkdirTemp(".", ".t-")
	if err != nil {
		panic("mkdir project-local testtmp: " + err.Error())
	}

	// Project-local TMPDIR is required by sandboxed CI for AF_UNIX
	// bind, but t.TempDir() returns it RELATIVE; some tests expect
	// substring matches against absolute paths, which fail. Pre-build
	// the absolute form so daemon tests can opt into either by
	// branching on filepath.IsAbs.
	setErr := os.Setenv("TMPDIR", tmp)
	if setErr != nil {
		panic("set TMPDIR: " + setErr.Error())
	}

	code := m.Run()

	removeErr := os.RemoveAll(tmp)
	if removeErr != nil {
		// Do not clobber a test failure with a cleanup error.
		_, _ = fmt.Fprintln(os.Stderr, "cleanup failed: "+removeErr.Error())
	}

	os.Exit(code)
}

// specFlags lists every flag v1 contract §7.7 requires on the usbipd root
// command. The assertion is that `--help` output contains each flag
// name. --config is intentionally absent: operators configure via
// flags + systemd drop-ins; YAML config is deferred to v2.
func specFlags() []string {
	return []string{
		"--listen",
		"--status-socket",
		"--status-socket-group",
		"--health-addr",
		"--allow-cidr",
		"--max-sessions",
		"--max-sessions-per-peer",
		"--accept-rate-limit",
		"--max-handshake-bytes",
		"--handshake-timeout",
		"--shutdown-timeout",
		"--log-level",
		"--log-format",
		"-v,",
	}
}

// TestRootHelpListsEveryFlag guards that `usbip serve --help` surfaces
// every flag required by v1 contract §7.7. The flags moved off the root
// onto the serve subcommand when the two-binary tree merged into the
// unified `usbip` binary (ADR-0011); the help assertion follows.
func TestRootHelpListsEveryFlag(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"serve", "--help"})

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	for _, flag := range specFlags() {
		require.Contains(t, out, flag, "serve --help missing flag %q", flag)
	}
}

// TestUnknownFlagReturnsExit2 confirms invalid flags surface as the
// cobra usage-error class that main maps onto exit code 2.
func TestUnknownFlagReturnsExit2(t *testing.T) {
	t.Parallel()

	code, err := runCtx(t.Context(), []string{"serve", "--no-such-flag"})
	require.Error(t, err)
	require.Equal(t, ExitUsage, code)
}

// TestConfigFlagRemoved confirms the --config flag is removed:
// --config must surface as a usage error (cobra "unknown flag"), not
// silently accepted. YAML config is deferred to v2; operators use
// flags + systemd drop-ins in v1.
func TestConfigFlagRemoved(t *testing.T) {
	t.Parallel()

	code, err := runCtx(t.Context(), []string{"--config", "/tmp/does-not-exist.yaml", "version"})
	require.Error(t, err)
	require.Equal(t, ExitUsage, code)
	require.Contains(t, err.Error(), "unknown flag",
		"expected cobra unknown-flag error, got %v", err)
}

// TestVersionSubcommand sanity-checks the version subcommand so the
// exit-code plumbing can be exercised without a running daemon.
func TestVersionSubcommand(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"version"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, buf.String(), "usbip-go version")
}
