// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"

	"github.com/abilisoft/usbip-go/pkg/domain"
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

// newPortCmd constructs the `usbip-go port [--id N]` subcommand (spec
// cli-interface OpenSpec).
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

	ports = activePorts(ports)

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

// activePorts removes vhci_hcd capacity rows that do not represent an active
// attachment. StatusNotAssigned is active: the kernel has claimed the vdev but
// has not assigned its USB address yet. The public ListPorts API deliberately
// exposes every kernel row, while the CLI's `port` and detach completion
// contracts describe active attachments only. Unknown future states remain
// visible instead of being silently discarded.
func activePorts(ports []usbip.Port) []usbip.Port {
	active := make([]usbip.Port, 0, len(ports))
	for _, port := range ports {
		if isAttachedPort(port) {
			active = append(active, port)
		}
	}

	return active
}

// filterPortByID returns the single-port slice and true when id identifies an
// attached port; nil and false when the slot is absent or free.
func filterPortByID(ports []usbip.Port, id usbip.PortID) ([]usbip.Port, bool) {
	for _, p := range ports {
		if p.ID == id && isAttachedPort(p) {
			return []usbip.Port{p}, true
		}
	}

	return nil, false
}

// isAttachedPort mirrors the kernel adapter's allocation boundary: null and
// available slots are free, while transitional, used, and error rows remain
// owned kernel attachments that operators may inspect or detach.
func isAttachedPort(port usbip.Port) bool {
	return port.Status != domain.StatusNull && port.Status != domain.StatusAvailable
}
