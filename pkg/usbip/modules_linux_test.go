//go:build linux

package usbip_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// chmodZero strips every mode bit from path; used to provoke EACCES on
// subsequent stat calls under path without relying on root or a real
// broken sysfs.
func chmodZero(path string) error {
	err := os.Chmod(path, 0)
	if err != nil {
		return fmt.Errorf("chmod zero %q: %w", path, err)
	}

	return nil
}

// chmodRestore undoes chmodZero so t.TempDir's recursive cleanup can
// walk into the directory.
func chmodRestore(path string) error {
	err := os.Chmod(path, 0o700)
	if err != nil {
		return fmt.Errorf("chmod restore %q: %w", path, err)
	}

	return nil
}

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

// TestProbeOneAtEACCESReturnsUnknown proves the Phase 8 Finding 5
// tri-state contract: a non-ENOENT stat error (the typical one being
// EACCES on a root directory with mode 0000) maps to Unknown, not
// Missing. The previous two-state design silently produced "missing"
// here, losing the "probe was blocked, not proven negative" signal.
func TestProbeOneAtEACCESReturnsUnknown(t *testing.T) {
	t.Parallel()

	// An unreadable directory yields EACCES on the nested stat below.
	// Chmod 0o000 strips every mode bit including owner read/execute.
	dir := t.TempDir()
	require.NoError(t, chmodZero(dir))

	t.Cleanup(func() {
		// Restore perms so t.TempDir cleanup can recurse in.
		_ = chmodRestore(dir)
	})

	state := usbip.ProbeOneAtForTest(dir, "usbip_core")
	require.Equal(t, usbip.ModuleStateUnknown, state,
		"EACCES under parent must classify as Unknown, got %q", state)
}
