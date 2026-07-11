// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package usbip_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"syscall"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestModuleStateMarshalJSON proves the tri-state ModuleState renders
// as a lowercase string matching the §7.7 status-JSON contract. The
// previous two-state design collapsed EACCES / EIO onto "missing"; the
// new Unknown value preserves that signal for operators.
func TestModuleStateMarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state usbip.ModuleState
		want  string
	}{
		{"loaded", usbip.ModuleStateLoaded, `"loaded"`},
		{"missing", usbip.ModuleStateMissing, `"missing"`},
		{"unknown", usbip.ModuleStateUnknown, `"unknown"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := json.Marshal(tt.state)
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

// TestProbeKernelModulesReturnsTriState proves ProbeKernelModules hands
// back a typed ModuleState map so callers cannot accidentally
// conflate a real "missing" with an "I couldn't tell". The per-module
// value is always one of the three Loaded/Missing/Unknown constants.
func TestProbeKernelModulesReturnsTriState(t *testing.T) {
	t.Parallel()

	mods, err := usbip.ProbeKernelModules(context.Background())
	require.NoError(t, err)
	require.Len(t, mods, 3,
		"probe must return the §11.5.4 triple")

	for name, state := range mods {
		switch state {
		case usbip.ModuleStateLoaded,
			usbip.ModuleStateMissing,
			usbip.ModuleStateUnknown:
			// OK — valid tri-state value.
		default:
			t.Errorf("module %q has unexpected state %q", name, state)
		}
	}
}

// TestProbeOneAtEACCESReturnsUnknown pins the tri-state module-probe
// contract: a non-ENOENT stat error (the typical one being EACCES on
// a root directory with mode 0000) maps to Unknown, not Missing. A
// two-state design would silently produce "missing" here, losing the
// "probe was blocked, not proven negative" signal.
func TestProbeOneAtEACCESReturnsUnknown(t *testing.T) {
	t.Parallel()

	// Inject a stat function that always returns EACCES. No chmod
	// dance, no t.TempDir cleanup hazard — just a direct simulation of
	// the "probe blocked" signal v1 contract §11.5.4 expects Unknown for.
	old := usbip.SwapProbeStatFnForTest(func(_ string) (fs.FileInfo, error) {
		return nil, syscall.EACCES
	})

	t.Cleanup(func() { usbip.SwapProbeStatFnForTest(old) })

	state := usbip.ProbeOneAtForTest("/any-root", usbip.KernelModuleUSBIPCore)
	require.Equal(t, usbip.ModuleStateUnknown, state,
		"EACCES under parent must classify as Unknown, got %q", state)
}
