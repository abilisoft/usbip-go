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

// listFlags bundles the list-subcommand compatibility flags. The preferred
// interface is `list` for local devices and `list HOST` for remote devices;
// the flags remain accepted so older scripts keep working.
type listFlags struct {
	Remote string
	Local  bool
	Ports  bool
}

// newListCmd constructs the `usbip-go list` subcommand per v1 contract §7.1.
func newListCmd() *cobra.Command {
	lf := &listFlags{}

	cmd := &cobra.Command{
		Use:   "list [remote]",
		Short: "List local or remote USB devices",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd, lf, args)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(
		&lf.Remote,
		"remote",
		"r",
		"",
		"query a remote USB/IP server (compatibility; prefer positional remote)",
	)
	flags.BoolVarP(
		&lf.Local,
		"local",
		"l",
		false,
		"list locally-exportable devices (compatibility; default when no remote is supplied)",
	)
	flags.BoolVarP(
		&lf.Ports,
		"ports",
		"p",
		false,
		"list attached vhci ports (compatibility; prefer the port command)",
	)

	cmd.MarkFlagsMutuallyExclusive("remote", "local", "ports")

	return cmd
}

// runList dispatches the list flavour based on the positional remote or the
// compatibility flags. With no selector it lists local exportable devices.
func runList(cmd *cobra.Command, lf *listFlags, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	r := pickRenderer(outputFromCtx(ctx))
	out := cmd.OutOrStdout()
	remoteFlagSet := cmd.Flags().Changed("remote")

	if len(args) == 1 && hasLegacyListSelector(lf, remoteFlagSet) {
		return errUsage("list: remote argument cannot be combined with --remote, --local, or --ports")
	}

	switch {
	case len(args) == 1:
		return runListRemote(ctx, r, out, args[0])
	case remoteFlagSet:
		return runListRemote(ctx, r, out, lf.Remote)
	case lf.Local:
		return runListLocal(ctx, r, out)
	case lf.Ports:
		return runListPorts(ctx, r, out)
	default:
		return runListLocal(ctx, r, out)
	}
}

func hasLegacyListSelector(lf *listFlags, remoteFlagSet bool) bool {
	return remoteFlagSet || lf.Local || lf.Ports
}

// runListRemote queries the remote endpoint and renders the device list.
func runListRemote(ctx context.Context, r Renderer, out ioWriter, remote string) error {
	ep, err := domain.ParseRemote(remote)
	if err != nil {
		return errUsage("invalid remote %q: %s", remote, err)
	}

	imp, err := newImporter(withLoggerFromCtx(ctx)...)
	if err != nil {
		return err
	}

	defer func() { _ = imp.Close() }()

	devs, err := imp.ListRemote(ctx, ep)
	if err != nil {
		return fmt.Errorf("list remote: %w", err)
	}

	err = r.Devices(out, devs)
	if err != nil {
		return fmt.Errorf("render devices: %w", err)
	}

	return nil
}

// runListLocal enumerates local exportable devices.
func runListLocal(ctx context.Context, r Renderer, out ioWriter) error {
	exp, err := newExporter(withExporterLoggerFromCtx(ctx)...)
	if err != nil {
		return err
	}

	devs, err := exp.ListAvailable(ctx)
	if err != nil {
		return fmt.Errorf("list local: %w", err)
	}

	err = r.Devices(out, devs)
	if err != nil {
		return fmt.Errorf("render devices: %w", err)
	}

	return nil
}

// runListPorts renders the currently-attached vhci ports.
func runListPorts(ctx context.Context, r Renderer, out ioWriter) error {
	imp, err := newImporter(withLoggerFromCtx(ctx)...)
	if err != nil {
		return err
	}

	defer func() { _ = imp.Close() }()

	ports, err := imp.ListPorts(ctx)
	if err != nil {
		return fmt.Errorf("list ports: %w", err)
	}

	err = r.Ports(out, ports)
	if err != nil {
		return fmt.Errorf("render ports: %w", err)
	}

	return nil
}

// ioWriter is a tiny alias that lets us keep the list.go imports lean.
type ioWriter = interface {
	Write(p []byte) (n int, err error)
}

// outputFromCtx reads the globalFlags.Output stashed by
// PersistentPreRunE. Defaults to "table" when missing.
func outputFromCtx(ctx context.Context) string {
	gf, ok := ctx.Value(flagsCtxKey).(*globalFlags)
	if !ok || gf == nil {
		return "table"
	}

	return gf.Output
}

// withLoggerFromCtx returns the ImporterOption slice carrying the
// context-stashed logger, if any. The variadic form keeps the factory
// call site clean whether a logger was installed or not.
func withLoggerFromCtx(ctx context.Context) []usbip.ImporterOption {
	log := loggerFromCtx(ctx)
	if log == nil {
		return nil
	}

	return []usbip.ImporterOption{usbip.WithImporterLogger(log)}
}

// withExporterLoggerFromCtx mirrors withLoggerFromCtx for exporters.
func withExporterLoggerFromCtx(ctx context.Context) []usbip.ExporterOption {
	log := loggerFromCtx(ctx)
	if log == nil {
		return nil
	}

	return []usbip.ExporterOption{usbip.WithExporterLogger(log)}
}
