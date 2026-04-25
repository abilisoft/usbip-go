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

// listFlags bundles the list-subcommand flags. Exactly one of Remote /
// Local / Ports must be set; cobra's MarkFlagsOneRequired +
// MarkFlagsMutuallyExclusive enforce the rule.
type listFlags struct {
	Remote string
	Local  bool
	Ports  bool
}

// newListCmd constructs the `usbip list` subcommand per spec §7.1.
func newListCmd() *cobra.Command {
	lf := &listFlags{}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List remote, local, or attached devices",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runList(cmd, lf)
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&lf.Remote, "remote", "r", "", "query a remote USB/IP server")
	flags.BoolVarP(&lf.Local, "local", "l", false, "list locally-exportable devices")
	flags.BoolVarP(&lf.Ports, "ports", "p", false, "list attached vhci ports")

	cmd.MarkFlagsOneRequired("remote", "local", "ports")
	cmd.MarkFlagsMutuallyExclusive("remote", "local", "ports")

	return cmd
}

// runList dispatches the list flavour based on which flag was set.
func runList(cmd *cobra.Command, lf *listFlags) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	r := pickRenderer(outputFromCtx(ctx))
	out := cmd.OutOrStdout()

	switch {
	case lf.Remote != "":
		return runListRemote(ctx, r, out, lf.Remote)
	case lf.Local:
		return runListLocal(ctx, r, out)
	case lf.Ports:
		return runListPorts(ctx, r, out)
	}

	// Unreachable: MarkFlagsOneRequired enforced by cobra before RunE.
	return errUsage("list: no subject flag set")
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
