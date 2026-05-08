// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Package main is the usbip-go client CLI entrypoint (v1 contract §7.1).
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/fang"
)

// applyNoColorEarly sets NO_COLOR=1 when argv contains --no-color in
// any form cobra accepts: bare `--no-color`, the explicit
// `--no-color=true`, or `--no-color=1`. fang's help renderer reads
// the environment before cobra's PersistentPreRunE fires, so the env
// mutation MUST happen prior to fang.Execute or
// `usbip-go --no-color --help` comes back colored despite the flag.
// Idempotent and side-effect-only.
func applyNoColorEarly(args []string) {
	for _, a := range args {
		if a == "--no-color" {
			_ = os.Setenv("NO_COLOR", "1")
			return
		}

		if !strings.HasPrefix(a, "--no-color=") {
			continue
		}

		v := strings.TrimPrefix(a, "--no-color=")
		// Cobra parses bool flag values via strconv.ParseBool; mirror
		// that here so `--no-color=false` does NOT enable the env.
		b, err := strconv.ParseBool(v)
		if err == nil && b {
			_ = os.Setenv("NO_COLOR", "1")
			return
		}
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)

	code, err := runCtx(ctx, os.Args[1:])

	renderMainError(os.Stderr, err)

	stop()
	os.Exit(code)
}

// renderMainError writes the operator-facing stderr line for err. The
// v1 contract §7.4 template is produced via FormatError so callers grepping
// on the canonical wording ("usbip-go: device not found", etc.) see it
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
//
// fang.Execute styles the help/error output via lipgloss while
// preserving cobra's exit semantics. We disable fang's built-in
// completion + version because we ship our own (with stamped
// metadata + per-shell installer); manpages are likewise skipped
// to keep the surface minimal. The custom error handler is a no-op
// because we render errors ourselves in main() via FormatError —
// fang's stylised error rendering would emit a duplicate line.
func runCtx(ctx context.Context, args []string) (int, error) {
	applyNoColorEarly(args)

	cmd := rootCmdFactory()
	cmd.SetArgs(args)

	err := fang.Execute(
		ctx, cmd,
		fang.WithoutCompletions(),
		fang.WithoutVersion(),
		fang.WithoutManpage(),
		fang.WithErrorHandler(func(io.Writer, fang.Styles, error) {}),
	)
	if err != nil {
		return MapError(err), err
	}

	return 0, nil
}
