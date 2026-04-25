// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootCmdHasNoDeadConfigFlag pins the removal of the --config
// flag on the `usbip` root command. The field was registered but
// never read, silently accepting any path operators supplied. A dead
// knob that looks alive is worse than no knob; YAML configuration
// was deferred past v1 per the cmd/usbipd policy, and the client
// gets the same treatment for consistency.
func TestRootCmdHasNoDeadConfigFlag(t *testing.T) {
	t.Parallel()

	root := newRootCmd()

	require.Nil(t, root.PersistentFlags().Lookup("config"),
		"root usbip must not register a persistent --config; YAML config is deferred past v1")
}
