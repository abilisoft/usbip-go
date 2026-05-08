// Package main is the usbip client CLI entrypoint (spec §7.1).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)

	code, err := runCtx(ctx, os.Args[1:])

	renderMainError(os.Stderr, err)

	stop()
	os.Exit(code)
}

// renderMainError writes the operator-facing stderr line for err. The
// spec §7.4 template is produced via FormatError so callers grepping
// on the canonical wording ("usbip: device not found", etc.) see it
// regardless of how deeply err was wrapped along its call path. nil
// errors produce no output.
func renderMainError(w io.Writer, err error) {
	if err == nil {
		return
	}

	_, _ = fmt.Fprintln(w, FormatError(err))
}

// rootCmdFactory produces the root cobra command runCtx dispatches
// against. Tests swap it to register probe subcommands without
// reaching into the cobra call stack; production keeps the default
// newRootCmd factory.
var rootCmdFactory = newRootCmd

// runCtx executes the root cobra command under ctx and returns the
// mapped exit code. Passing a cancellable ctx lets every subcommand
// observe shutdown via cmd.Context, which matters for long-running
// calls (attach --auto-reconnect, list -r, bind) that would
// otherwise block in a kernel or network call past Ctrl-C.
func runCtx(ctx context.Context, args []string) (int, error) {
	cmd := rootCmdFactory()
	cmd.SetArgs(args)

	err := cmd.ExecuteContext(ctx)
	if err != nil {
		return MapError(err), err
	}

	return 0, nil
}
