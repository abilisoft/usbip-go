package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
	"golang.org/x/term"
)

// traceLevel matches cmd/usbip's convention — slog.LevelDebug-4 so
// trace sits below debug. cmd/usbip owns its own constant so the two
// binaries can diverge independently; the numeric value is identical.
const traceLevel = slog.LevelDebug - 4

// errInvalidLogLevel and errInvalidLogFormat mirror cmd/usbip's
// sentinels for the same flag values. Duplicated rather than shared
// because both binaries treat the error class as part of their own
// observable CLI contract.
var (
	errInvalidLogLevel  = errors.New("invalid --log-level")
	errInvalidLogFormat = errors.New("invalid --log-format")
)

// buildLogger constructs the daemon's structured logger from cfg. The
// handler selection mirrors cmd/usbip: auto picks tint on a TTY and
// JSON otherwise; pretty and json are explicit overrides.
func buildLogger(cfg Config) (*slog.Logger, error) {
	lvl, err := parseLevel(cfg.LogLevel, cfg.VerboseCount)
	if err != nil {
		return nil, err
	}

	isTTY := isStderrTTY()
	noColor := os.Getenv("NO_COLOR") != ""

	switch cfg.LogFormat {
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
		return nil, fmt.Errorf("%w %q (want auto, pretty, or json)", errInvalidLogFormat, cfg.LogFormat)
	}
}

// newTintLogger builds a slog.Logger backed by the lmittmann/tint
// handler. Matches cmd/usbip.newTintLogger byte-for-byte.
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

// isStderrTTY reports whether os.Stderr is a terminal. The uintptr→int
// narrowing is guarded for gosec G115 — same shape as cmd/usbip.
func isStderrTTY() bool {
	fd := os.Stderr.Fd()
	if fd > uintptr(^uint(0)>>1) {
		return false
	}

	return term.IsTerminal(int(fd))
}

// parseLevel resolves a textual level name to a slog.Level and then
// applies the -v counter promotion rules.
func parseLevel(name string, verbose int) (slog.Level, error) {
	base, err := baseLevel(name)
	if err != nil {
		return 0, err
	}

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
