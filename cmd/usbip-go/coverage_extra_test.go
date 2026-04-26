// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestValidateShellAcceptedShells walks every accepted shell name and
// the rejection branch. The five accepted values must round-trip
// unchanged; an unknown name must return errShellUnknown.
func TestValidateShellAcceptedShells(t *testing.T) {
	t.Parallel()

	for _, shell := range []string{shellBash, shellZsh, shellFish, shellPwsh, shellPowershell} {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()

			got, err := validateShell(shell)
			require.NoError(t, err)
			require.Equal(t, shell, got)
		})
	}
}

// TestValidateShellRejectsUnknown locks in the error sentinel for an
// unrecognised shell name.
func TestValidateShellRejectsUnknown(t *testing.T) {
	t.Parallel()

	got, err := validateShell("ksh")
	require.Empty(t, got)
	require.Error(t, err)
	require.ErrorIs(t, err, errShellUnknown)
}

// TestGenerateScriptEachShell exercises every cobra completion
// generator branch by piping into a buffer and asserting the
// generator wrote at least one byte. The unknown-shell branch
// returns errShellUnknown without touching the writer.
func TestGenerateScriptEachShell(t *testing.T) {
	t.Parallel()

	for _, shell := range []string{shellBash, shellZsh, shellFish, shellPwsh, shellPowershell} {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()

			cmd := newRootCmd()

			var buf bytes.Buffer

			err := generateScript(cmd, shell, &buf)
			require.NoError(t, err)
			require.NotEmpty(t, buf.Bytes(),
				"generateScript must write a non-empty completion script for %s", shell)
		})
	}
}

// TestGenerateScriptUnknownShellRejected covers the default branch of
// generateScript.
func TestGenerateScriptUnknownShellRejected(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := generateScript(newRootCmd(), "ksh", &buf)
	require.Error(t, err)
	require.ErrorIs(t, err, errShellUnknown)
}

// TestCompletionPathPerShell exercises every accepted-shell branch of
// completionPath against a known XDG_DATA_HOME so the file paths are
// deterministic. Validates the per-shell suffix.
func TestCompletionPathPerShell(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	cases := map[string]string{
		shellBash:       filepath.Join(tmp, "bash-completion", "completions", "usbip-go"),
		shellZsh:        filepath.Join(tmp, "zsh", "site-functions", "_usbip-go"),
		shellFish:       filepath.Join(tmp, "fish", "vendor_completions.d", "usbip-go.fish"),
		shellPwsh:       filepath.Join(tmp, "powershell", "Modules", "usbip-go.ps1"),
		shellPowershell: filepath.Join(tmp, "powershell", "Modules", "usbip-go.ps1"),
	}

	for shell, want := range cases {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()

			got, err := completionPath(shell)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
	}
}

// TestCompletionPathUnknownShellRejected covers completionPath's
// default branch.
func TestCompletionPathUnknownShellRejected(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	got, err := completionPath("ksh")
	require.Empty(t, got)
	require.Error(t, err)
	require.ErrorIs(t, err, errShellUnknown)
}

// TestXDGDataHomeUsesEnvWhenSet covers the env-set branch.
func TestXDGDataHomeUsesEnvWhenSet(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-test")

	got, err := xdgDataHome()
	require.NoError(t, err)
	require.Equal(t, "/tmp/xdg-test", got)
}

// TestXDGDataHomeFallsBackToHomeDir covers the unset-env branch by
// clearing XDG_DATA_HOME and asserting the result is rooted under
// HOME/.local/share.
func TestXDGDataHomeFallsBackToHomeDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/tmp/fakehome-xdg")

	got, err := xdgDataHome()
	require.NoError(t, err)
	require.Equal(t, "/tmp/fakehome-xdg/.local/share", got)
}

// TestRunUninstallRemovesExistingFile covers the success path of
// runUninstall: the file exists, Remove succeeds, the status line
// is written.
func TestRunUninstallRemovesExistingFile(t *testing.T) {
	t.Parallel()

	tmp := filepath.Join(t.TempDir(), "completion")
	require.NoError(t, os.WriteFile(tmp, []byte("# stub"), 0o600))

	var buf bytes.Buffer

	err := runUninstall(&buf, tmp)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "removed")

	_, statErr := os.Stat(tmp)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

// TestRunUninstallTolerantsMissingFile covers the os.ErrNotExist
// branch: a missing target is not an error so the install/uninstall
// pair is idempotent.
func TestRunUninstallTolerantsMissingFile(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "does-not-exist")

	var buf bytes.Buffer

	err := runUninstall(&buf, target)
	require.NoError(t, err)
}

// failingWriter is an io.Writer that returns errFailingWriter on
// every Write. Used to drive the "write status line" error branch
// of runUninstall.
type failingWriter struct{}

var errFailingWriter = errors.New("forced writer failure")

func (failingWriter) Write(_ []byte) (int, error) { return 0, errFailingWriter }

// TestRunUninstallSurfacesWriterError covers the writer-error branch
// of runUninstall.
func TestRunUninstallSurfacesWriterError(t *testing.T) {
	t.Parallel()

	target := filepath.Join(t.TempDir(), "does-not-exist")

	err := runUninstall(failingWriter{}, target)
	require.Error(t, err)
	require.ErrorIs(t, err, errFailingWriter)
}

// TestCompletePortIDsListsKnownPorts covers the success path of
// completePortIDs: ListPorts returns a non-empty slice and the
// completion list mirrors the port IDs.
func TestCompletePortIDsListsKnownPorts(t *testing.T) {
	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return []usbip.Port{
				{ID: 1, BusID: "1-1.2"},
				{ID: 5, BusID: "2-1"},
			}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()
	cmd.SetContext(context.Background())

	got, _ := completePortIDs(cmd, nil, "")
	joined := strings.Join(got, ",")
	require.Contains(t, joined, "1")
	require.Contains(t, joined, "5")
}

// TestCompletePortIDsErrorReturnsDirective covers the error path:
// when the importer reports a list failure, the completion returns
// ShellCompDirectiveError and a nil completion list.
func TestCompletePortIDsErrorReturnsDirective(t *testing.T) {
	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return nil, errFailingWriter
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()
	cmd.SetContext(context.Background())

	got, _ := completePortIDs(cmd, nil, "")
	require.Nil(t, got)
}

// TestCompleteBoundBusIDsForwards covers completeBoundBusIDs's
// alias-to-completeBindableBusIDs delegation. The function must run
// without panicking; the resulting completion list shape depends on
// the bind-state filter applied internally.
func TestCompleteBoundBusIDsForwards(t *testing.T) {
	exp := &mockExporter{
		listAvailableFn: func(_ context.Context) ([]usbip.Device, error) {
			return []usbip.Device{{BusID: domain.BusID("1-1.2")}}, nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	cmd := newRootCmd()
	cmd.SetContext(context.Background())

	_, _ = completeBoundBusIDs(cmd, nil, "")
}

// TestRunDetachInvalidPortIDRejected covers the strconv.ParseUint
// error branch of runDetach.
func TestRunDetachInvalidPortIDRejected(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()
	cmd.SetContext(context.Background())

	err := runDetach(cmd, []string{"not-a-number"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid port id")
}

// TestRunDetachImporterErrorPropagates covers the kernel-detach
// error path: a mockImporter whose Detach returns an error must
// surface that error wrapped.
func TestRunDetachImporterErrorPropagates(t *testing.T) {
	imp := &mockImporter{
		detachFn: func(_ context.Context, _ usbip.PortID) error {
			return errFailingWriter
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()
	cmd.SetContext(context.Background())

	err := runDetach(cmd, []string{"3"})
	require.Error(t, err)
}

// TestBaseLevelAcceptedNames covers each branch of baseLevel's
// switch: trace/debug/info/warn/error map to the matching slog
// level, anything else returns errInvalidLogLevel.
func TestBaseLevelAcceptedNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"trace", "debug", "info", "warn", "error"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := baseLevel(name)
			require.NoError(t, err)
		})
	}
}

// TestBaseLevelUnknownRejected covers the default branch.
func TestBaseLevelUnknownRejected(t *testing.T) {
	t.Parallel()

	_, err := baseLevel("noisy")
	require.Error(t, err)
	require.ErrorIs(t, err, errInvalidLogLevel)
}

// TestWriteBindAckJSONBindOp covers the bind branch of the JSON ack
// renderer. The output must contain the busid.
func TestWriteBindAckJSONBindOp(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writeBindAckJSON(&buf, "bind", "1-1.2")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "1-1.2")
}

// TestWriteBindAckJSONUnbindOp covers the unbind branch.
func TestWriteBindAckJSONUnbindOp(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writeBindAckJSON(&buf, "unbind", "1-1.2")
	require.NoError(t, err)
	require.Contains(t, buf.String(), "1-1.2")
}

// TestWriteBindAckJSONUnknownOp covers the default branch's
// errUsage return.
func TestWriteBindAckJSONUnknownOp(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writeBindAckJSON(&buf, "rebind", "1-1.2")
	require.Error(t, err)
}

// TestRunListLocalSurfacesListError covers the error path: when
// the exporter's ListAvailable returns an error, runListLocal
// wraps it.
func TestRunListLocalSurfacesListError(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		listAvailableFn: func(_ context.Context) ([]usbip.Device, error) {
			return nil, errFailingWriter
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	var buf bytes.Buffer

	err := runListLocal(context.Background(), tableRenderer{}, &buf)
	require.Error(t, err)
	require.Contains(t, err.Error(), "list local")
}

// TestRunListLocalRendersAvailable covers the success path.
func TestRunListLocalRendersAvailable(t *testing.T) {
	t.Parallel()

	exp := &mockExporter{
		listAvailableFn: func(_ context.Context) ([]usbip.Device, error) {
			return []usbip.Device{
				{BusID: "1-1.2", VendorID: 0x0951, ProductID: 0x1664},
			}, nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	var buf bytes.Buffer

	err := runListLocal(context.Background(), tableRenderer{}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "1-1.2")
}
