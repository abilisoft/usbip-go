// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main_test

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/testutil"
)

// TestUSBIPGoBinary_HelpListsAllSubcommands pins the public command
// surface visible from the help text. Catches a regression where a
// subcommand is dropped, renamed, or hidden — all of which would break
// scripts and operator muscle memory.
func TestUSBIPGoBinary_HelpListsAllSubcommands(t *testing.T) {
	t.Parallel()

	bin := buildUsbipGoBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "--help").CombinedOutput()
	require.NoError(t, err, "--help must exit 0; got: %s", out)

	// Cobra renders subcommands with a fixed two-space indent at the
	// start of each "Available Commands:" line. A bare substring match
	// would let "bind" pass on the strength of "unbind"; the
	// indent-anchored regex pins the actual subcommand name.
	help := string(out)
	for _, cmd := range []string{
		"attach", "detach", "bind", "unbind", "list", "port",
		"watch", "completion", "version",
	} {
		re := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(cmd) + `\s`)
		require.Regexp(t, re, help,
			"--help must list subcommand %q on its own line so operators can discover it", cmd)
	}
}

// TestUSBIPGoBinary_VersionEmitsBuildMetadata pins that the version
// subcommand renders the stamped build metadata (version, commit,
// build date, Go runtime). Operators rely on this for triage so a
// regression that drops any field is operator-visible.
func TestUSBIPGoBinary_VersionEmitsBuildMetadata(t *testing.T) {
	t.Parallel()

	bin := buildUsbipGoBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "version").CombinedOutput()
	require.NoError(t, err, "version must exit 0; got: %s", out)

	got := string(out)
	require.Contains(t, got, "usbip-go version")
	require.Contains(t, got, "commit")
	require.Contains(t, got, "built")
	require.Contains(t, got, "go1.")
}

// TestUSBIPGoBinary_RejectsInvalidOutputFlag pins the flag-validation
// path: an invalid --output value must produce a non-zero exit and an
// error message naming the bad value, not silently default to table.
func TestUSBIPGoBinary_RejectsInvalidOutputFlag(t *testing.T) {
	t.Parallel()

	bin := buildUsbipGoBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "--output", "xml", "version").CombinedOutput()
	require.Error(t, err,
		"unrecognized --output value must fail, not silently fall through")

	combined := string(out)
	require.Contains(t, combined, "xml",
		"error must name the rejected value so the operator sees what was wrong; got: %s", combined)
}

// TestUSBIPGoBinary_CompletionEmitsBashScript pins that
// `completion bash` produces a non-empty bash completion script
// containing the command name. Operators run this through `source` so
// silent regressions to empty output break tab-completion immediately.
func TestUSBIPGoBinary_CompletionEmitsBashScript(t *testing.T) {
	t.Parallel()

	bin := buildUsbipGoBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, bin, "completion", "bash").CombinedOutput()
	require.NoError(t, err, "completion bash must exit 0; got: %s", out)

	script := string(out)
	require.Contains(t, script, "usbip-go",
		"completion script must reference the command name")
	require.True(t, strings.Contains(script, "complete") || strings.Contains(script, "_usbip_go"),
		"output must look like a bash completion script; got first 200 chars: %s",
		firstN(script, 200))
}

func firstN(s string, n int) string {
	if len(s) < n {
		return s
	}

	return s[:n]
}

// buildUsbipGoBinary compiles ./cmd/usbip-go into an absolute-path
// temp binary, returning the path. Thin wrapper over the canonical
// testutil.BuildBinary so a regression to the build flags lands in
// one place.
func buildUsbipGoBinary(t *testing.T) string {
	t.Helper()

	return testutil.BuildBinary(t, "usbip-go")
}
