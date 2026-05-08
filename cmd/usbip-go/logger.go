// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"golang.org/x/term"
)

// traceLevel is the project's trace log level — one step below debug.
// slog does not ship a named Trace level; spec §7.2 pins the label so
// we encode it as slog.LevelDebug - 4 per slog convention.
const traceLevel = slog.LevelDebug - 4

// errInvalidLogFormat is the sentinel base for an unrecognised
// --log-format value; wrapped with the offending value so the caller
// sees both the classification and the concrete input.
var errInvalidLogFormat = errors.New("invalid --log-format")

// errInvalidLogLevel is the sentinel base for an unrecognised
// --log-level value.
var errInvalidLogLevel = errors.New("invalid --log-level")

// buildLogger constructs a *slog.Logger whose handler is selected by
// the format flag and TTY/NO_COLOR state (spec §7.3). An invalid
// --log-format surfaces as an error so the root PersistentPreRunE can
// fail with a usage-class message.
func buildLogger(f globalFlags) (*slog.Logger, error) {
	lvl, err := parseLevel(f.LogLevel, f.VerboseCount)
	if err != nil {
		return nil, err
	}

	isTTY := isStderrTTY()
	noColor := os.Getenv("NO_COLOR") != "" || f.NoColor

	switch f.LogFormat {
	case "auto":
		if isTTY && !noColor {
			return newTintLogger(lvl, noColor), nil
		}

		return newJSONLogger(lvl), nil
	case "pretty":
		return newTintLogger(lvl, noColor), nil
	case "json":
		return newJSONLogger(lvl), nil
	default:
		return nil, fmt.Errorf("%w %q (want auto, pretty, or json)", errInvalidLogFormat, f.LogFormat)
	}
}

// newTintLogger builds a slog.Logger backed by the lmittmann/tint
// handler. noColor suppresses ANSI escapes (tint.Options.NoColor).
func newTintLogger(lvl slog.Level, noColor bool) *slog.Logger {
	return slog.New(tint.NewHandler(os.Stderr, &tint.Options{
		Level:   lvl,
		NoColor: noColor,
	}))
}

// newJSONLogger builds a slog.Logger backed by the stdlib JSON handler.
func newJSONLogger(lvl slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: lvl,
	}))
}

// isStderrTTY reports whether os.Stderr is a terminal. The helper
// encapsulates the uintptr→int narrowing so gosec's G115 (integer
// overflow on conversion) sees a bounds check instead of a bare cast.
// Stderr's file descriptor is a small non-negative integer on every
// platform Go supports; the check only rejects the theoretical overflow
// case by treating a too-large fd as "not a TTY".
func isStderrTTY() bool {
	fd := os.Stderr.Fd()
	if fd > uintptr(^uint(0)>>1) { // exceeds max int
		return false
	}

	return term.IsTerminal(int(fd))
}

// parseLevel resolves a textual level name to a slog.Level, then applies
// the -v counter: count=1 promotes anything below debug to debug,
// count>=2 promotes to trace. Explicit --log-level=trace still yields
// trace regardless of counter.
func parseLevel(name string, verbose int) (slog.Level, error) {
	base, err := baseLevel(name)
	if err != nil {
		return 0, err
	}

	// -v counter only promotes (lowers numeric threshold). It never
	// raises a user-selected trace back to debug.
	if verbose >= 2 && base > traceLevel {
		return traceLevel, nil
	}

	if verbose >= 1 && base > slog.LevelDebug {
		return slog.LevelDebug, nil
	}

	return base, nil
}

// baseLevel is the pure name→level lookup used by parseLevel.
func baseLevel(name string) (slog.Level, error) {
	switch name {
	case "trace":
		return traceLevel, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("%w %q (want error/warn/info/debug/trace)", errInvalidLogLevel, name)
	}
}
