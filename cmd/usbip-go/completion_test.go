// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestCompletionBashScript — `completion bash` prints a cobra-generated
// bash completion script whose header contains "bash completion".
func TestCompletionBashScript(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"completion", "bash"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(out.String()), "bash completion")
}

// TestCompletionZshScript — zsh variant.
func TestCompletionZshScript(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"completion", "zsh"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(out.String()), "zsh")
}

// TestCompletionBindDynamic — the bind ValidArgsFunction queries the
// mocked Exporter.ListAvailable and returns each busid.
func TestCompletionBindDynamic(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		listAvailableFn: func(_ context.Context) ([]usbip.Device, error) {
			return []usbip.Device{{BusID: "1-1.2", VendorID: 0xabcd, ProductID: 0x0001}}, nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	completions, directive := completeBindableBusIDs(newRootCmd(), nil, "")
	require.Equal(t, []string{"1-1.2\tabcd:0001"}, cobraCompletionStrings(completions))
	require.NotZero(t, directive)
}

// TestCompletionInstallDryRun — `completion install --shell=zsh
// --dry-run` prints the would-be path to stderr (we capture both).
// Uses t.Setenv so it cannot run parallel with other env-sensitive
// tests; runs serialised via factoriesMu acquired by swapFactories.
func TestCompletionInstallDryRun(t *testing.T) {
	swapFactories(t, &mockImporter{}, &mockExporter{})

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("HOME", tmp)

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"completion", "install", "--shell=zsh", "--dry-run"})

	err := cmd.Execute()
	require.NoError(t, err)
	// Dry-run emits a target path somewhere in the buffer; assert the
	// tmp dir substring appears.
	require.Contains(t, out.String(), tmp)
}

// TestCompletionInstallWrites — non-dry-run writes the zsh script to
// the target path.
func TestCompletionInstallWrites(t *testing.T) {
	swapFactories(t, &mockImporter{}, &mockExporter{})

	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)
	t.Setenv("HOME", tmp)

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"completion", "install", "--shell=zsh"})

	err := cmd.Execute()
	require.NoError(t, err)

	// Find the written file under tmp.
	found := false

	walkErr := filepath.Walk(tmp, func(path string, info os.FileInfo, _ error) error {
		if info != nil && !info.IsDir() && strings.Contains(path, "usbip") {
			found = true
		}

		return nil
	})
	require.NoError(t, walkErr)
	require.True(t, found, "expected installed completion file under %s", tmp)
}

// TestCompletionInstallUndetectable — when --shell is empty and $SHELL
// is unreadable, exit with usage and a specific message.
func TestCompletionInstallUndetectable(t *testing.T) {
	swapFactories(t, &mockImporter{}, &mockExporter{})

	t.Setenv("SHELL", "")

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"completion", "install"})

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unable to detect shell")
}

// cobraCompletionStrings is a tiny shim so tests can compare
// []cobra.Completion (currently a []string alias) without reaching
// into cobra internals.
func cobraCompletionStrings(c []string) []string { return c }
