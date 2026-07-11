// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunCtx_NoColorAppliedBeforeHelp pins that --no-color sets
// NO_COLOR in the environment BEFORE fang renders the help screen.
// Cobra's PersistentPreRunE only runs for the matched leaf command;
// `--help` never matches a leaf, so the env mutation in the
// PersistentPreRunE hook never fires for help. This test catches a
// regression where the help text comes back colored despite the
// flag, which would force operators with non-truecolor terminals
// (CI logs, ssh sessions on legacy terms) to copy-paste through ANSI.
func TestRunCtx_NoColorAppliedBeforeHelp(t *testing.T) {
	// t.Setenv unsets after the test so the assertion is independent
	// of test ordering; we want runCtx to set it itself.
	require.NoError(t, os.Unsetenv("NO_COLOR"))

	t.Cleanup(func() {
		_ = os.Unsetenv("NO_COLOR")
	})

	code, _ := runCtx(context.Background(), []string{"--no-color", testHelpFlag})
	require.Zero(t, code, "--help must exit cleanly")

	require.Equal(t, "1", os.Getenv("NO_COLOR"),
		"--no-color must propagate to the environment before fang renders help, not after PersistentPreRunE")
}

// TestRunCtx_NoColorEqualsFormAlsoApplied pins that cobra's
// --no-color=true (the bool-with-explicit-value form) is treated
// the same as --no-color. argv-scanning that only matches the
// bare token would let `--no-color=true --help` come back colored.
func TestRunCtx_NoColorEqualsFormAlsoApplied(t *testing.T) {
	require.NoError(t, os.Unsetenv("NO_COLOR"))

	t.Cleanup(func() {
		_ = os.Unsetenv("NO_COLOR")
	})

	code, _ := runCtx(context.Background(), []string{"--no-color=true", testHelpFlag})
	require.Zero(t, code, "--help must exit cleanly")

	require.Equal(t, "1", os.Getenv("NO_COLOR"),
		"--no-color=true must propagate to NO_COLOR the same way --no-color does")
}
