// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/spf13/cobra"
)

// newListCmd constructs the `usbip-go list` subcommand per v1 contract §7.1.
func newListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list [remote]",
		Short: "List local or remote USB devices",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runList,
	}

	return cmd
}

// runList dispatches based on the optional positional remote. With no remote
// it lists local exportable devices.
func runList(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	r := pickRenderer(outputFromCtx(ctx))
	out := cmd.OutOrStdout()

	if len(args) == 1 {
		return runListRemote(ctx, r, out, args[0])
	}

	return runListLocal(ctx, r, out)
}

// runListRemote queries the remote endpoint and renders the device list.
func runListRemote(ctx context.Context, r Renderer, out io.Writer, remote string) error {
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
func runListLocal(ctx context.Context, r Renderer, out io.Writer) error {
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
