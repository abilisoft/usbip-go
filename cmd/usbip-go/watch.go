// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// newWatchCmd constructs the `usbip-go watch` subcommand. Watch emits one
// jsonlines record per event when --output=json; the table renderer
// emits human-readable lines. SIGINT / SIGTERM cancel cleanly.
func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "watch",
		Short: "Stream USB/IP domain events until interrupted",
		Args:  cobra.NoArgs,
		RunE:  runWatch,
	}
}

// runWatch streams events from Importer.Watch through the selected
// renderer until the iterator closes or the signal context cancels.
func runWatch(cmd *cobra.Command, _ []string) error {
	parent := cmd.Context()
	if parent == nil {
		parent = context.Background()
	}

	ctx, cancel := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	imp, err := newImporter(withLoggerFromCtx(ctx)...)
	if err != nil {
		return err
	}

	defer func() { _ = imp.Close() }()

	r := pickRenderer(outputFromCtx(ctx))
	out := cmd.OutOrStdout()

	for ev := range imp.Watch(ctx) {
		err = r.Event(out, ev)
		if err != nil {
			return fmt.Errorf("render event: %w", err)
		}
	}

	return nil
}
