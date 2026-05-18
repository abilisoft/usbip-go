// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/spf13/cobra"
)

// newBindCmd constructs the `usbip-go bind <busid>` subcommand.
func newBindCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bind <busid>",
		Short: "Bind a local device to usbip-host for export",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBindUnbind(cmd, args[0], "bind")
		},
	}

	cmd.ValidArgsFunction = completeBindableBusIDs

	return cmd
}

// newUnbindCmd constructs the `usbip-go unbind <busid>` subcommand.
func newUnbindCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unbind <busid>",
		Short: "Unbind a local device from usbip-host",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBindUnbind(cmd, args[0], "unbind")
		},
	}

	cmd.ValidArgsFunction = completeBoundBusIDs

	return cmd
}

// runBindUnbind dispatches to the matching Exporter method by op name.
func runBindUnbind(cmd *cobra.Command, raw, op string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	busID, err := domain.ParseBusID(raw)
	if err != nil {
		return errUsage("invalid busid %q: %s", raw, err)
	}

	exp, err := newExporter(withExporterLoggerFromCtx(ctx)...)
	if err != nil {
		return err
	}

	switch op {
	case "bind":
		err = exp.Bind(ctx, busID)
	case "unbind":
		err = exp.Unbind(ctx, busID)
	default:
		return errUsage("unknown op %q", op)
	}

	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}

	return writeBindAck(cmd, op, busID)
}

// writeBindAck renders the post-Bind/Unbind confirmation.
func writeBindAck(cmd *cobra.Command, op string, busID domain.BusID) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	out := cmd.OutOrStdout()

	if outputFromCtx(ctx) == outputJSON {
		err := writeBindAckJSON(out, op, busID)
		if err != nil {
			return fmt.Errorf("render ack: %w", err)
		}

		return nil
	}

	_, err := fmt.Fprintln(styleWriter(out), formatAck(op+"ed", string(busID)))
	if err != nil {
		return fmt.Errorf("write output: %w", err)
	}

	return nil
}

// writeBindAckJSON dispatches to the correct typed ack method by op
// name. bind and unbind each have their own envelope type so the JSON
// shape is locked per op; a typed switch keeps the dispatch obvious.
func writeBindAckJSON(w io.Writer, op string, busID domain.BusID) error {
	switch op {
	case "bind":
		return (jsonRenderer{}).BindAck(w, busID)
	case "unbind":
		return (jsonRenderer{}).UnbindAck(w, busID)
	default:
		return errUsage("unknown ack op %q", op)
	}
}

// completeBindableBusIDs lists locally-available devices that are not
// already bound. Errors are silent (completion is best-effort).
func completeBindableBusIDs(
	cmd *cobra.Command,
	_ []string,
	_ string,
) ([]cobra.Completion, cobra.ShellCompDirective) {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	exp, err := newExporter(withExporterLoggerFromCtx(ctx)...)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	devs, err := exp.ListAvailable(ctx)
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}

	out := make([]cobra.Completion, 0, len(devs))

	for _, d := range devs {
		out = append(out, fmt.Sprintf("%s\t%04x:%04x", d.BusID, d.VendorID, d.ProductID))
	}

	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeBoundBusIDs lists devices currently bound to usbip-host. The
// current public facade does not yet distinguish bound vs available, so
// we reuse ListAvailable as the closest approximation; this preserves
// v1 contract §7.6 behaviour once the facade grows a dedicated method.
func completeBoundBusIDs(
	cmd *cobra.Command,
	_ []string,
	_ string,
) ([]cobra.Completion, cobra.ShellCompDirective) {
	return completeBindableBusIDs(cmd, nil, "")
}
