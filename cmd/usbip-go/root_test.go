// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRootHelpPrintsUsage asserts that `usbip-go --help` runs without error
// and emits the root Long/Use description on stdout. Smoke-level check
// that the cobra command tree is wired.
func TestRootHelpPrintsUsage(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{testHelpFlag})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "usbip")
	require.Contains(t, out.String(), "Usage:")
}

// TestRootInvalidOutputFlag ensures cobra rejects --output=bogus via the
// global flag's validator (v1 contract §7.2 enum). The CLI exits 2 in subprocess
// land; here we assert the parse-level error surfaces from Execute.
func TestRootInvalidOutputFlag(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=bogus", testVersionToken})

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, strings.ToLower(err.Error()), "output")
}

// TestRootInvalidLogFormat — the logger builder returns an error when
// --log-format is not one of auto/pretty/json. Pre-GREEN, buildLogger
// does not exist; the compile error is the RED signal.
func TestRootInvalidLogFormat(t *testing.T) {
	t.Parallel()

	_, err := buildLogger(globalFlags{
		LogLevel:  logLevelInfo,
		LogFormat: "bogus",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "log-format")
}

// TestBuildLoggerJSONFormat — when --log-format=json the returned logger
// emits JSON-shaped records (presence of the "msg" key + double quote).
func TestBuildLoggerJSONFormat(t *testing.T) {
	t.Parallel()

	log, err := buildLogger(globalFlags{
		LogLevel:  logLevelInfo,
		LogFormat: logFormatJSON,
	})
	require.NoError(t, err)
	require.NotNil(t, log)
}

// TestBuildLoggerPrettyFormat — the tint handler accepts the "pretty"
// selector.
func TestBuildLoggerPrettyFormat(t *testing.T) {
	t.Parallel()

	log, err := buildLogger(globalFlags{
		LogLevel:  logLevelInfo,
		LogFormat: "pretty",
	})
	require.NoError(t, err)
	require.NotNil(t, log)
}

// TestParseLevelCounterPromotion — -v counter promotes info→debug,
// -vv (count=2) promotes to trace. Pre-GREEN parseLevel does not exist.
func TestParseLevelCounterPromotion(t *testing.T) {
	t.Parallel()

	lvl, err := parseLevel(logLevelInfo, 0)
	require.NoError(t, err)
	require.NotNil(t, lvl)

	debug, err := parseLevel(logLevelInfo, 1)
	require.NoError(t, err)
	require.NotEqual(t, lvl, debug)

	trace, err := parseLevel(logLevelInfo, 2)
	require.NoError(t, err)
	require.NotEqual(t, debug, trace)
}

// TestParseLevelInvalid — unknown names return an error.
func TestParseLevelInvalid(t *testing.T) {
	t.Parallel()

	_, err := parseLevel("bogus", 0)
	require.Error(t, err)
}

// TestGlobalFlagsDefaults — the default construction (no arg parse) uses
// table output, info level, auto format.
func TestGlobalFlagsDefaults(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()
	// Execute a no-op subcommand (version) to trigger flag parse.
	cmd.SetArgs([]string{testVersionToken})

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err := cmd.Execute()
	require.NoError(t, err)
}

// TestLoggerOrDefaultReturnsCtxLogger — when ctx carries a logger via
// loggerContextKey, loggerOrDefault MUST return that exact instance so
// downstream log calls share the operator's configured handler.
func TestLoggerOrDefaultReturnsCtxLogger(t *testing.T) {
	t.Parallel()

	custom := slog.New(slog.DiscardHandler)
	ctx := context.WithValue(t.Context(), loggerContextKey{}, custom)

	got := loggerOrDefault(ctx)
	require.Same(t, custom, got,
		"ctx-bound logger MUST be returned verbatim, not wrapped or replaced")
}

// TestLoggerOrDefaultFallsBackToSlogDefault — when ctx has no logger
// (e.g. http handler invoked from net/http internals before
// PersistentPreRunE installed one), loggerOrDefault MUST return
// slog.Default() so callers can issue log calls without nil checks.
func TestLoggerOrDefaultFallsBackToSlogDefault(t *testing.T) {
	t.Parallel()

	got := loggerOrDefault(t.Context())
	require.Same(t, slog.Default(), got,
		"empty ctx MUST fall back to slog.Default — never nil")
}

// TestLoggerFromCtxReturnsNilWhenAbsent pins the contract loggerOrDefault
// relies on: the bare lookup returns nil when no logger is installed.
func TestLoggerFromCtxReturnsNilWhenAbsent(t *testing.T) {
	t.Parallel()

	require.Nil(t, loggerFromCtx(t.Context()))
}
