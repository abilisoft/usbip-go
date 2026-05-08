// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// version, commit, and buildDate mirror cmd/usbip-go's layout so the same
// -ldflags stamping flow populates both binaries.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

// newVersionCmd returns the `usbipd-go version` subcommand.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version metadata",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := styleWriter(cmd.OutOrStdout())

			line := actionStyle.Render("usbipd-go version") + " " +
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
