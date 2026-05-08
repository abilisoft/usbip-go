package main

import (
	"context"
	"fmt"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/spf13/cobra"
)

// portFlags bundles the `port` subcommand flags.
type portFlags struct {
	// ID filters the output to a single port id when >=0.
	// pflag exposes Changed() so we can distinguish "unset" from
	// "set to 0" without needing a *int sentinel.
	ID uint32
}

// newPortCmd constructs the `usbip port [--id N]` subcommand (spec
// §7.1).
func newPortCmd() *cobra.Command {
	pf := &portFlags{}

	cmd := &cobra.Command{
		Use:   "port",
		Short: "List currently-attached vhci ports",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPort(cmd, pf)
		},
	}

	cmd.Flags().Uint32Var(&pf.ID, "id", 0, "filter to a specific port id")

	return cmd
}

// runPort lists ports (or filters to one).
func runPort(cmd *cobra.Command, pf *portFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	imp, err := newImporter(withLoggerFromCtx(ctx)...)
	if err != nil {
		return err
	}

	defer func() { _ = imp.Close() }()

	ports, err := imp.ListPorts(ctx)
	if err != nil {
		return fmt.Errorf("list ports: %w", err)
	}

	if cmd.Flags().Changed("id") {
		filtered, found := filterPortByID(ports, usbip.PortID(pf.ID))
		if !found {
			return fmt.Errorf("port %d is not attached: %w", pf.ID, usbip.ErrDeviceNotFound)
		}

		ports = filtered
	}

	r := pickRenderer(outputFromCtx(ctx))

	err = r.Ports(cmd.OutOrStdout(), ports)
	if err != nil {
		return fmt.Errorf("render ports: %w", err)
	}

	return nil
}

// filterPortByID returns the single-port slice and true when id is in
// ports; nil and false when absent.
func filterPortByID(ports []usbip.Port, id usbip.PortID) ([]usbip.Port, bool) {
	for _, p := range ports {
		if p.ID == id {
			return []usbip.Port{p}, true
		}
	}

	return nil, false
}
