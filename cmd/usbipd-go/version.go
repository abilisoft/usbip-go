// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// version, commit, and buildDate mirror cmd/usbip's layout so the same
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
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"usbipd version %s (commit %s, built %s, %s)\n",
				version, commit, buildDate, runtime.Version())
			if err != nil {
				return fmt.Errorf("write version output: %w", err)
			}

			return nil
		},
	}
}
