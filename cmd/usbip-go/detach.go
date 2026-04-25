// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"strconv"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/spf13/cobra"
)

// newDetachCmd constructs the `usbip-go detach <port>` subcommand.
func newDetachCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "detach <port>",
		Short: "Detach a previously-attached vhci port",
		Args:  cobra.ExactArgs(1),
		RunE:  runDetach,
	}

	cmd.ValidArgsFunction = completePortIDs

	return cmd
}

// runDetach parses the port id and tears down the attachment.
func runDetach(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	pidU, err := strconv.ParseUint(args[0], 10, 32)
	if err != nil {
		return errUsage("invalid port id %q: %s", args[0], err)
	}

	imp, err := newImporter(withLoggerFromCtx(ctx)...)
	if err != nil {
		return err
	}

	defer func() { _ = imp.Close() }()

	err = imp.Detach(ctx, usbip.PortID(pidU))
	if err != nil {
		return fmt.Errorf("detach: %w", err)
	}

	out := cmd.OutOrStdout()

	if outputFromCtx(ctx) == outputJSON {
		err = (jsonRenderer{}).DetachAck(out, usbip.PortID(pidU))
		if err != nil {
			return fmt.Errorf("render ack: %w", err)
		}

		return nil
	}

	_, err = fmt.Fprintf(out, "detached port %d\n", pidU)
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// completePortIDs is the dynamic ValidArgsFunction for `detach`. It
// queries Importer.ListPorts and returns each attached port id as a
// completion. Errors are silent (completion is best-effort).
func completePortIDs(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	imp, err := newImporter(withLoggerFromCtx(ctx)...)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	defer func() { _ = imp.Close() }()

	ports, err := imp.ListPorts(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	out := make([]cobra.Completion, 0, len(ports))

	for _, p := range ports {
		out = append(out, fmt.Sprintf("%d\t%s %s", p.ID, p.Remote.String(), p.BusID))
	}

	return out, cobra.ShellCompDirectiveNoFileComp
}
