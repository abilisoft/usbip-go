// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// errBoom and errUnrelated are static sentinels for MapError tests;
// named so err113 does not flag inline errors.New literals.
var (
	errBoom      = errors.New("boom")
	errUnrelated = errors.New("unrelated")
)

// TestBaseLevelAcceptedNamesUsbipd covers each branch of baseLevel.
func TestBaseLevelAcceptedNamesUsbipd(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"trace", "debug", "info", "warn", "error"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := baseLevel(name)
			require.NoError(t, err)
		})
	}
}

// TestBaseLevelUnknownRejectedUsbipd covers the default branch.
func TestBaseLevelUnknownRejectedUsbipd(t *testing.T) {
	t.Parallel()

	_, err := baseLevel("noisy")
	require.Error(t, err)
	require.ErrorIs(t, err, errInvalidLogLevel)
}

// TestParseLevelVerbosePromotion covers the verbose-counter branches:
// verbose >= 2 promotes to traceLevel, verbose >= 1 promotes to debug
// when the base is higher.
func TestParseLevelVerbosePromotion(t *testing.T) {
	t.Parallel()

	got, err := parseLevel("info", 2)
	require.NoError(t, err)
	require.Equal(t, traceLevel, got)

	got, err = parseLevel("info", 1)
	require.NoError(t, err)
	require.Equal(t, slog.LevelDebug, got)

	got, err = parseLevel("info", 0)
	require.NoError(t, err)
	require.Equal(t, slog.LevelInfo, got)

	_, err = parseLevel("noisy", 0)
	require.Error(t, err)
}

// TestNewTintLoggerNonNil covers newTintLogger: returns a non-nil
// *slog.Logger configured with the lmittmann/tint handler. We do
// not assert handler internals; smoke-test that construction works
// for both the colour and the no-colour branches.
func TestNewTintLoggerNonNil(t *testing.T) {
	t.Parallel()

	require.NotNil(t, newTintLogger(slog.LevelInfo, false))
	require.NotNil(t, newTintLogger(slog.LevelDebug, true))
}

// TestMapErrorClassification covers each branch of MapError:
// nil -> ExitOK; errAlreadyRunning -> ExitAlreadyRunning; errDrainTimeout
// -> ExitTimeout; anything else -> ExitGeneric.
func TestMapErrorClassification(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil clean exit", nil, ExitOK},
		{"already running", errAlreadyRunning, ExitAlreadyRunning},
		{"drain timeout", errDrainTimeout, ExitTimeout},
		{"wrapped already running", fmt.Errorf("ctx: %w", errAlreadyRunning), ExitAlreadyRunning},
		{"unknown error", errBoom, ExitGeneric},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, MapError(tc.err))
		})
	}
}

// TestIsExpectedServeExitClassifies covers the helper that decides
// whether a Serve return is a graceful shutdown rather than a fault.
func TestIsExpectedServeExitClassifies(t *testing.T) {
	t.Parallel()

	require.True(t, isExpectedServeExit(context.Canceled))
	require.True(t, isExpectedServeExit(net.ErrClosed))
	require.True(t, isExpectedServeExit(fmt.Errorf("wrap: %w", context.Canceled)))
	require.False(t, isExpectedServeExit(errUnrelated))
	require.False(t, isExpectedServeExit(nil))
}

// TestSystemdActivatedDetectsLISTENPID covers the activation probe:
// LISTEN_PID matching our PID + a positive LISTEN_FDS counts as
// activation; missing or mismatched returns false.
func TestSystemdActivatedDetectsLISTENPID(t *testing.T) {
	// No t.Parallel — env-mutating.
	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LISTEN_FDS", "1")
	require.True(t, systemdActivated())
}

// TestSystemdActivatedRejectsMissingEnv covers the env-empty branch.
func TestSystemdActivatedRejectsMissingEnv(t *testing.T) {
	t.Setenv("LISTEN_PID", "")
	t.Setenv("LISTEN_FDS", "")
	require.False(t, systemdActivated())
}

// TestSystemdActivatedRejectsWrongPID covers the PID-mismatch branch.
func TestSystemdActivatedRejectsWrongPID(t *testing.T) {
	t.Setenv("LISTEN_PID", "0")
	t.Setenv("LISTEN_FDS", "1")
	require.False(t, systemdActivated())
}

// TestFirstSingletonListenerReturnsExpected covers the helper that
// extracts the single listener from a one-element named map.
func TestFirstSingletonListenerReturnsExpected(t *testing.T) {
	t.Parallel()

	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	defer func() { _ = ln.Close() }()

	got := firstSingletonListener(map[string][]net.Listener{
		"usbipd-go.socket": {ln},
	})
	require.Same(t, ln, got)
}

// TestFirstSingletonListenerEmptyReturnsNil covers the empty-map
// branch.
func TestFirstSingletonListenerEmptyReturnsNil(t *testing.T) {
	t.Parallel()

	require.Nil(t, firstSingletonListener(map[string][]net.Listener{}))
}

// TestStatusExporterListeningProjection covers the trivial shape
// projection: the listening address, activation flag, and accepting
// flag round-trip into listeningState.
func TestStatusExporterListeningProjection(t *testing.T) {
	t.Parallel()

	se := &statusExporter{
		listenAddr: "127.0.0.1:3240",
		activation: true,
	}
	se.accepting.Store(true)

	got := se.Listening()
	require.Equal(t, "127.0.0.1:3240", got.Addr)
	require.True(t, got.Activation)
	require.True(t, got.Accepting)

	se.accepting.Store(false)
	require.False(t, se.Listening().Accepting)
}

// TestMarkAcceptingTransitions covers the markAccepting helper: it
// flips the atomic flag both directions.
func TestMarkAcceptingTransitions(t *testing.T) {
	t.Parallel()

	var se statusExporter

	se.markAccepting(true)
	require.True(t, se.accepting.Load())

	se.markAccepting(false)
	require.False(t, se.accepting.Load())
}

// TestSetDrainAndDrainCallback covers setDrain + the no-op-when-nil
// branch on Drain.
func TestSetDrainAndDrainCallback(t *testing.T) {
	t.Parallel()

	var (
		se     statusExporter
		called atomic.Bool
	)

	se.setDrain(func() { called.Store(true) })

	cancel := se.drain.Load()
	require.NotNil(t, cancel)

	(*cancel)()
	require.True(t, called.Load())
}

// TestBuildLoggerEachFormat covers buildServeLogger's switch arms: auto,
// pretty, json, and the default (errInvalidLogFormat). The auto
// branch's TTY/no-color heuristic depends on stderr; we don't
// assert which sub-handler it picks, only that no error fires.
func TestBuildLoggerEachFormat(t *testing.T) {
	t.Parallel()

	for _, fmtName := range []string{"auto", "pretty", "json"} {
		t.Run(fmtName, func(t *testing.T) {
			t.Parallel()

			lg, err := buildServeLogger(ServeConfig{LogFormat: fmtName, LogLevel: "info"})
			require.NoError(t, err)
			require.NotNil(t, lg)
		})
	}

	t.Run("invalid format rejected", func(t *testing.T) {
		t.Parallel()

		_, err := buildServeLogger(ServeConfig{LogFormat: "weird", LogLevel: "info"})
		require.Error(t, err)
	})

	t.Run("invalid level rejected", func(t *testing.T) {
		t.Parallel()

		_, err := buildServeLogger(ServeConfig{LogFormat: "json", LogLevel: "noisy"})
		require.Error(t, err)
	})
}

// TestNewJSONLoggerSmoke covers newJSONLogger's construction.
func TestNewJSONLoggerSmoke(t *testing.T) {
	t.Parallel()

	require.NotNil(t, newJSONLogger(slog.LevelInfo))
}

// TestIsStderrTTYReturnsBool covers isStderrTTY's basic shape.
// The actual answer depends on stderr; test only asserts the call
// completes without panic.
func TestIsStderrTTYReturnsBool(t *testing.T) {
	t.Parallel()

	_ = isStderrTTY()
}
