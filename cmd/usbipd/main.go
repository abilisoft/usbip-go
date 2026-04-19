// Package main is the usbipd daemon entrypoint (spec §7.7).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	code, err := runWithContext(ctx, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}

	os.Exit(code)
}

// run is the testable entrypoint — no os.Exit, no signal wiring — used
// by cmd/usbipd's unit tests. Production code goes through main which
// wraps run with signal.NotifyContext + os.Exit.
func run(args []string) (int, error) {
	return runWithContext(context.Background(), args)
}

// runWithContext executes the root cobra command against the provided
// context. Extracting the context seam lets main install signal
// cancellation while tests pass context.Background for deterministic
// behaviour.
func runWithContext(ctx context.Context, args []string) (int, error) {
	cmd := newRootCmd()
	cmd.SetArgs(args)
	cmd.SetContext(ctx)

	err := cmd.Execute()
	if err != nil {
		return mapError(err), err
	}

	return exitOK, nil
}
