// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

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

	return renderDetachResult(cmd.OutOrStdout(), outputFromCtx(ctx), usbip.PortID(pidU))
}

// renderDetachResult writes the detach acknowledgement using the
// renderer selected by --output. Extracted from runDetach so the
// human-table path is unit-testable without a live importer.
func renderDetachResult(out io.Writer, format string, pid usbip.PortID) error {
	if format == outputJSON {
		err := (jsonRenderer{}).DetachAck(out, pid)
		if err != nil {
			return fmt.Errorf("render ack: %w", err)
		}

		return nil
	}

	_, err := fmt.Fprintln(styleWriter(out), formatAck("detached port", strconv.FormatUint(uint64(pid), 10)))
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

	ports = activePorts(ports)

	out := make([]cobra.Completion, 0, len(ports))

	for _, p := range ports {
		description := strings.TrimSpace(strings.Join([]string{
			formatRemoteEndpoint(p.Remote),
			string(p.BusID),
		}, " "))

		completion := strconv.FormatUint(uint64(p.ID), 10)
		if description != "" {
			completion += "\t" + description
		}

		out = append(out, completion)
	}

	return out, cobra.ShellCompDirectiveNoFileComp
}
