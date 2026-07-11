// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// TestLeafCommandsRejectUnexpectedArguments prevents typo operands from
// silently starting a daemon, draining it, or changing completion
// files.
func TestLeafCommandsRejectUnexpectedArguments(t *testing.T) {
	t.Parallel()

	root := &cobra.Command{Use: "usbip-go"}
	cases := []struct {
		name string
		cmd  *cobra.Command
	}{
		{name: testServeCommand, cmd: newServeCmd()},
		{name: "drain", cmd: newDrainCmd()},
		{name: testVersionToken, cmd: newVersionCmd()},
		{name: "completion install", cmd: newCompletionInstallCmd(root)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Error(t, tc.cmd.Args(tc.cmd, []string{"unexpected"}))
		})
	}
}
