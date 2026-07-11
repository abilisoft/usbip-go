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
	"github.com/spf13/cobra"
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
	cmd.SetArgs([]string{testCompletionCommand, "bash"})

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
	cmd.SetArgs([]string{testCompletionCommand, "zsh"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(out.String()), "zsh")
}

// TestCompletionBindDynamic — bind completion excludes devices already
// exported through usbip-host.
func TestCompletionBindDynamic(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		listAvailableFn: func(_ context.Context) ([]usbip.Device, error) {
			return []usbip.Device{
				{BusID: testNestedBusID, VendorID: 0xabcd, ProductID: 0x0001},
				{BusID: testSecondaryBusID, VendorID: 0x1234, ProductID: 0x5678},
			}, nil
		},
		listExportedFn: func(_ context.Context) ([]usbip.Device, error) {
			return []usbip.Device{{BusID: testSecondaryBusID}}, nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	completions, directive := completeBindableBusIDs(newRootCmd(), nil, "")
	require.Equal(t, []string{"1-1.2\tabcd:0001"}, cobraCompletionStrings(completions))
	require.NotZero(t, directive)
}

func TestCompletionUnbindDynamic(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		listExportedFn: func(_ context.Context) ([]usbip.Device, error) {
			return []usbip.Device{{BusID: testSecondaryBusID, VendorID: 0x1234, ProductID: 0x5678}}, nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	completions, directive := completeBoundBusIDs(newRootCmd(), nil, "")
	require.Equal(t, []string{"2-1\t1234:5678"}, cobraCompletionStrings(completions))
	require.Equal(t, cobra.ShellCompDirectiveNoFileComp, directive)
}

func TestCompletionBindErrorsReturnErrorDirective(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		exporter *mockExporter
	}{
		{
			name: "list available",
			exporter: &mockExporter{
				listAvailableFn: func(_ context.Context) ([]usbip.Device, error) {
					return nil, errTest
				},
			},
		},
		{
			name: "list exported",
			exporter: &mockExporter{
				listAvailableFn: func(_ context.Context) ([]usbip.Device, error) {
					return []usbip.Device{sampleDevice()}, nil
				},
				listExportedFn: func(_ context.Context) ([]usbip.Device, error) {
					return nil, errTest
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			swapFactories(t, &mockImporter{}, test.exporter)

			completions, directive := completeBindableBusIDs(newRootCmd(), nil, "")
			require.Nil(t, completions)
			require.Equal(t, completionErrorDirective, directive)
		})
	}
}

func TestCompletionUnbindListErrorReturnsErrorDirective(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		listExportedFn: func(_ context.Context) ([]usbip.Device, error) {
			return nil, errTest
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	completions, directive := completeBoundBusIDs(newRootCmd(), nil, "")
	require.Nil(t, completions)
	require.Equal(t, completionErrorDirective, directive)
}

func TestCompletionExporterConstructionErrorsReturnErrorDirective(t *testing.T) {
	t.Parallel()

	factoriesMu.Lock()

	original := newExporter

	newExporter = func(_ ...usbip.ExporterOption) (Exporter, error) {
		return nil, errTest
	}

	t.Cleanup(func() {
		newExporter = original
		factoriesMu.Unlock()
	})

	bindCompletions, bindDirective := completeBindableBusIDs(newRootCmd(), nil, "")
	require.Nil(t, bindCompletions)
	require.Equal(t, completionErrorDirective, bindDirective)

	unbindCompletions, unbindDirective := completeBoundBusIDs(newRootCmd(), nil, "")
	require.Nil(t, unbindCompletions)
	require.Equal(t, completionErrorDirective, unbindDirective)
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
	cmd.SetArgs([]string{testCompletionCommand, testInstallCommand, "--shell=zsh", "--dry-run"})

	err := cmd.Execute()
	require.NoError(t, err)
	// Dry-run emits a target path somewhere in the buffer; assert the
	// tmp dir substring appears. Strip any leading "./" so a project-
	// local TMPDIR (set by TestMain for AF_UNIX bind compatibility)
	// matches the dry-run renderer's filepath.Clean form.
	wantSub := strings.TrimPrefix(tmp, "./")
	require.Contains(t, out.String(), wantSub)
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
	cmd.SetArgs([]string{testCompletionCommand, testInstallCommand, "--shell=zsh"})

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
	cmd.SetArgs([]string{testCompletionCommand, testInstallCommand})

	err := cmd.Execute()
	require.Error(t, err)
	require.Contains(t, err.Error(), "unable to detect shell")
}

// cobraCompletionStrings is a tiny shim so tests can compare
// []cobra.Completion (currently a []string alias) without reaching
// into cobra internals.
func cobraCompletionStrings(c []string) []string { return c }
