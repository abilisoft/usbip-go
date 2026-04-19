package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// specFlags lists every flag spec §7.7 requires on the usbipd root
// command. The assertion is that `--help` output contains each flag name.
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
		"--drain-timeout",
		"--log-level",
		"--log-format",
		"--config",
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

// TestConfigNonexistentReturnsExit1 confirms --config <missing> surfaces
// as a generic failure (exit 1) after the parse succeeds — the config
// loader must stat the path and fail loudly.
func TestConfigNonexistentReturnsExit1(t *testing.T) {
	t.Parallel()

	code, err := run([]string{"--config", "/tmp/does-not-exist-usbipd-test.yaml", "version"})
	require.Error(t, err)
	require.Equal(t, exitGeneric, code)
	require.True(t,
		strings.Contains(err.Error(), "config") ||
			strings.Contains(err.Error(), "does-not-exist-usbipd-test"),
		"error should mention config path: %v", err)
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
