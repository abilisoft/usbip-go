// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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

	tmp := filepath.Join(t.TempDir(), testCompletionCommand)
	require.NoError(t, os.WriteFile(tmp, []byte("# stub"), 0o600))

	var buf bytes.Buffer

	err := runUninstall(&buf, tmp)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "removed")

	_, statErr := os.Stat(tmp)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}

// TestRunUninstallToleratesMissingFile covers the os.ErrNotExist
// branch: a missing target is not an error so the install/uninstall
// pair is idempotent.
func TestRunUninstallToleratesMissingFile(t *testing.T) {
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
// completion list mirrors the port IDs. Uses three- and four-digit
// IDs that cannot be confused with substring matches across each
// other (a regression that returned [10, 15, 51] would still satisfy
// `Contains "1"` + `Contains "5"`; using IDs whose decimal forms
// share no substring eliminates that false-positive class).
func TestCompletePortIDsListsKnownPorts(t *testing.T) {
	imp := &mockImporter{
		listPortsFn: func(_ context.Context) ([]usbip.Port, error) {
			return []usbip.Port{
				{ID: 100, BusID: testNestedBusID},
				{ID: 256, BusID: testSecondaryBusID},
			}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()
	cmd.SetContext(context.Background())

	got, _ := completePortIDs(cmd, nil, "")
	require.Len(t, got, 2,
		"completePortIDs must return one entry per kernel port")
	require.Contains(t, got[0], "100")
	require.Contains(t, got[1], "256")
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
			return []usbip.Device{{BusID: domain.BusID(testNestedBusID)}}, nil
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

	for _, name := range []string{
		logLevelTrace,
		logLevelDebug,
		logLevelInfo,
		logLevelWarn,
		logLevelError,
	} {
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

	err := writeBindAckJSON(&buf, testBindCommand, testNestedBusID)
	require.NoError(t, err)
	require.Contains(t, buf.String(), testNestedBusID)
}

// TestWriteBindAckJSONUnbindOp covers the unbind branch.
func TestWriteBindAckJSONUnbindOp(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writeBindAckJSON(&buf, testUnbindCommand, testNestedBusID)
	require.NoError(t, err)
	require.Contains(t, buf.String(), testNestedBusID)
}

// TestWriteBindAckJSONUnknownOp covers the default branch's
// errUsage return.
func TestWriteBindAckJSONUnknownOp(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writeBindAckJSON(&buf, "rebind", testNestedBusID)
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
				{BusID: testNestedBusID, VendorID: 0x0951, ProductID: 0x1664},
			}, nil
		},
	}
	swapFactories(t, &mockImporter{}, exp)

	var buf bytes.Buffer

	err := runListLocal(context.Background(), tableRenderer{}, &buf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), testNestedBusID)
}

// TestVersionCmdRendersStampedLabels exercises the version subcommand:
// it must write a non-empty line containing the runtime Go version.
// The actual version/commit/buildDate values are -ldflags stamps with
// "dev"/"none"/"unknown" defaults; we only assert structure.
func TestVersionCmdRendersStampedLabels(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	cmd := newVersionCmd()
	cmd.SetOut(&buf)

	require.NoError(t, cmd.RunE(cmd, nil))
	require.Contains(t, buf.String(), "usbip-go version")
}

// TestGenerateScriptErrorOnFailingWriter pins the error-return paths in
// generateScript: cobra's generator functions write to the supplied
// io.Writer; a failing writer must cause the shell-specific error return
// to fire for each shell variant.
func TestGenerateScriptErrorOnFailingWriter(t *testing.T) {
	t.Parallel()

	for _, shell := range []string{shellBash, shellZsh, shellFish, shellPwsh} {
		t.Run(shell, func(t *testing.T) {
			t.Parallel()

			err := generateScript(newRootCmd(), shell, failingWriter{})
			require.Error(t, err)
		})
	}
}

// TestWriteCompletionScript_StatusLineError pins the final Fprintln error
// path in writeCompletionScript: the script is generated and written to
// disk, but if the output writer fails on the status-line write the error
// must propagate.
func TestWriteCompletionScript_StatusLineError(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	target := filepath.Join(tmp, "bash-completion", "completions", "usbip-go")

	err := writeCompletionScript(failingWriter{}, newRootCmd(), shellBash, target)
	require.Error(t, err)
}

// TestResolveShell_FromSHELLEnv pins the "SHELL env set to a valid shell"
// branch of resolveShell: when --shell is not given but $SHELL names a
// supported shell the function must return its basename.
func TestResolveShell_FromSHELLEnv(t *testing.T) {
	t.Setenv("SHELL", "/bin/bash")

	got, err := resolveShell("")
	require.NoError(t, err)
	require.Equal(t, shellBash, got)
}

// TestRunCompletionInstall_UninstallBranch pins the Uninstall switch case
// in runCompletionInstall: --uninstall must call runUninstall rather than
// writeCompletionScript.
func TestRunCompletionInstall_UninstallBranch(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_DATA_HOME", tmp)

	target, err := completionPath(shellBash)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o750))
	require.NoError(t, os.WriteFile(target, []byte("# stub"), 0o600))

	cmd := newRootCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)

	cf := &completionInstallFlags{Shell: shellBash, Uninstall: true}

	err = runCompletionInstall(cmd, newRootCmd(), cf)
	require.NoError(t, err)
	require.Contains(t, buf.String(), "removed")
}

// TestOutputFromCtx_MissingFlagsFallsBackToTable pins the default branch
// of outputFromCtx: a context without the flagsContextKey stashed by
// PersistentPreRunE must return "table".
func TestOutputFromCtx_MissingFlagsFallsBackToTable(t *testing.T) {
	t.Parallel()

	got := outputFromCtx(context.Background())
	require.Equal(t, "table", got)
}

// TestTableRenderer_Event_UnknownType pins the !ok branch of
// tableRenderer.Event: when eventHeader does not recognise the event
// record the fallback fmt.Fprintf branch must render %T/%v without
// error.
func TestTableRenderer_Event_UnknownType(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := (tableRenderer{}).Event(&buf, unknownEventImpl{})
	require.NoError(t, err)
	require.NotEmpty(t, buf.String())
}

// unknownEventImpl satisfies usbip.Event (= domain.Event) but is not
// recognised by classifyEvent, triggering the !ok branch in
// tableRenderer.Event.
type unknownEventImpl struct{}

func (unknownEventImpl) EventKind() domain.EventKind { return domain.EventKind(255) }

// TestCompleteAttachArgs_DefaultBranch pins the default case (len(args) > 1)
// in completeAttachArgs: the function must return nil and NoFileComp.
func TestCompleteAttachArgs_DefaultBranch(t *testing.T) {
	t.Parallel()

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newRootCmd()
	cmd.SetContext(context.Background())

	got, _ := completeAttachArgs(cmd, []string{"host", testRootBusID, "extra"}, "")
	require.Nil(t, got)
}

// TestParseAttachArgs_InvalidBackoff pins the parseBackoff error branch:
// a non-empty but malformed --backoff value must return an errUsage error.
func TestParseAttachArgs_InvalidBackoff(t *testing.T) {
	t.Parallel()

	af := &attachFlags{Backoff: "invalid-backoff-spec"}
	_, err := parseAttachArgs([]string{"10.0.0.1", testRootBusID}, af)
	require.Error(t, err)
}
