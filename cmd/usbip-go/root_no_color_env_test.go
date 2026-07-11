// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPersistentPreRunE_NoColorSetsEnv pins that PersistentPreRunE
// propagates --no-color to the environment by calling
// os.Setenv("NO_COLOR", "1"). The test runs the version subcommand
// (which triggers PersistentPreRunE via Cobra's persistent hook chain)
// rather than --help, because Cobra skips PersistentPreRunE for the
// built-in help handler.
func TestPersistentPreRunE_NoColorSetsEnv(t *testing.T) {
	// Not parallel: mutates process environment.
	require.NoError(t, os.Unsetenv("NO_COLOR"))

	t.Cleanup(func() { _ = os.Unsetenv("NO_COLOR") })

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--no-color", testVersionToken})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Equal(t, "1", os.Getenv("NO_COLOR"),
		"PersistentPreRunE must call os.Setenv when --no-color is set so lipgloss/colorprofile sees it")
}
