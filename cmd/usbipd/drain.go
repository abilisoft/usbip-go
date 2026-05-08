package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// errDrainNotImplemented is the placeholder error returned by the drain
// subcommand until Task 8.5 lands.
var errDrainNotImplemented = errors.New("usbipd drain: not implemented")

// newDrainCmd registers the `usbipd drain` subcommand. Task 8.5 flips
// RunE to the HTTP-over-UDS implementation; 8.1 only needs the command
// to exist so the cobra tree compiles.
func newDrainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drain",
		Short: "Request the running usbipd to refuse new accepts and exit",
		RunE: func(_ *cobra.Command, _ []string) error {
			return errDrainNotImplemented
		},
	}
}
