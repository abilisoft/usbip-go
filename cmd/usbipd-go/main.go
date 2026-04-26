// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Package main is the usbipd daemon entrypoint (v1 contract §7.7).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/fang"
)

func main() {
	os.Exit(mainBody())
}

// mainBody holds the real startup so defer + os.Exit coexist cleanly:
// main() defers nothing and forwards the helper's return code, while
// mainBody's own defers (signal.NotifyContext stop) run before the
// process terminates.
func mainBody() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	code, err := runWithContext(ctx, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	return code
}

// run is the testable entrypoint — no os.Exit, no signal wiring — used
// by cmd/usbipd-go's unit tests. Production code goes through main which
// wraps run with signal.NotifyContext + os.Exit.
func run(args []string) (int, error) {
	return runWithContext(context.Background(), args)
}

// runWithContext executes the root cobra command against the provided
// context. Extracting the context seam lets main install signal
// cancellation while tests pass context.Background for deterministic
// behaviour.
//
// fang.Execute wraps cobra's executor to render styled help / version /
// completions. Manpages and built-in version are disabled because we
// ship our own version subcommand (with stamped build metadata) and
// keep the daemon surface minimal. The custom error handler is a
// no-op because mainBody renders the error itself; fang's stylised
// error rendering would otherwise emit a duplicate line.
func runWithContext(ctx context.Context, args []string) (int, error) {
	cmd := newRootCmd()
	cmd.SetArgs(args)
	cmd.SetContext(ctx)

	err := fang.Execute(ctx, cmd,
		fang.WithoutManpage(),
		fang.WithoutVersion(),
		fang.WithErrorHandler(func(io.Writer, fang.Styles, error) {}),
	)
	if err != nil {
		return mapError(err), err
	}

	return exitOK, nil
}
