package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

// version, commit, and buildDate are stamped in via -ldflags at release
// time. Tests exercise the zero-value defaults; release builds override
// them from goreleaser.
var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

// newVersionCmd returns the `usbip version` subcommand which prints
// the stamped build metadata.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version metadata",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(),
				"usbip version %s (commit %s, built %s, %s)\n",
				version, commit, buildDate, runtime.Version())
			if err != nil {
				return fmt.Errorf("write version output: %w", err)
			}

			return nil
		},
	}
}
