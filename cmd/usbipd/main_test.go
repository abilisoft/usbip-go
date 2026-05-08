package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMain redirects the test-scoped tmpdir to a project-local
// directory. The default $TMPDIR (/tmp under the Claude Code sandbox)
// refuses AF_UNIX socket creation — the seccomp filter blocks unix
// bind(2) outside the project root, even though /tmp is in allowWrite.
// Pointing t.TempDir() at ./.test-tmp-* keeps every UDS bind inside the
// project directory that the sandbox does permit. Removed after m.Run.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp(".", ".test-tmp-")
	if err != nil {
		panic("mkdir project-local testtmp: " + err.Error())
	}

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

// specFlags lists every flag spec §7.7 requires on the usbipd root
// command. The assertion is that `--help` output contains each flag
// name. --config was removed in Phase 8 review (Finding 6): operators
// configure via flags + systemd drop-ins; YAML config is deferred to v2.
func specFlags() []string {
	return []string{
		"--listen",
		"--status-socket",
		"--status-socket-group",
		"--metrics-addr",
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

// TestRootHelpListsEveryFlag guards that the root `--help` surfaces
// every flag required by spec §7.7. cobra renders flags as `--foo` in
// --help regardless of short form; the -v counter is detected via the
// trailing comma in "-v, --verbose".
func TestRootHelpListsEveryFlag(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	require.NoError(t, err)

	out := buf.String()
	for _, flag := range specFlags() {
		require.Contains(t, out, flag, "--help missing flag %q", flag)
	}
}

// TestUnknownFlagReturnsExit2 confirms invalid flags surface as the
// cobra usage-error class that main maps onto exit code 2.
func TestUnknownFlagReturnsExit2(t *testing.T) {
	t.Parallel()

	code, err := run([]string{"--no-such-flag"})
	require.Error(t, err)
	require.Equal(t, exitUsage, code)
}

// TestConfigFlagRemoved confirms Phase 8 Finding 6's removal: --config
// must surface as a usage error (cobra "unknown flag"), not silently
// accepted. YAML config is deferred to v2; operators use flags +
// systemd drop-ins in v1.
func TestConfigFlagRemoved(t *testing.T) {
	t.Parallel()

	code, err := run([]string{"--config", "/tmp/does-not-exist.yaml", "version"})
	require.Error(t, err)
	require.Equal(t, exitUsage, code)
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
	require.Contains(t, buf.String(), "usbipd version")
}
