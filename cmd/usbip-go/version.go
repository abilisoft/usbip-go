// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// version, commit, and buildDate are stamped through linker definitions at
// release time. Bazel release builds source them from stable workspace status;
// GoReleaser supplies the same variables through -ldflags.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

// newVersionCmd returns the `usbip-go version` subcommand which prints
// the stamped build metadata.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version metadata",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := styleWriter(cmd.OutOrStdout())

			line := actionStyle.Render("usbip-go version") + " " +
				subjectStyle.Render(version) +
				dimStyle.Render(fmt.Sprintf(" (commit %s, built %s, %s)",
					commit, buildDate, runtime.Version()))

			_, err := fmt.Fprintln(out, line)
			if err != nil {
				return fmt.Errorf("write version output: %w", err)
			}

			return nil
		},
	}
}
