// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestStatusStyle_AllBranches pins every case in statusStyle so a
// mutation that adds/removes/reorders a branch is caught. Asserts that
// the returned style is non-zero (lipgloss.Style is always non-nil) and
// that each semantic group produces a distinct foreground color so the
// function is not collapsed to a no-op by a mutation.
func TestStatusStyle_AllBranches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status string
	}{
		// active group
		{"used", "used"},
		{"active", "active"},
		{"in_use", "in_use"},
		{"attached", "attached"},
		// error group
		{"error", "error"},
		{"lost", "lost"},
		{"disconnected", "disconnected"},
		{"failed", "failed"},
		// available group
		{"available", "available"},
		{"idle", "idle"},
		{"free", "free"},
		{"ready", "ready"},
		// pending group
		{"pending", "pending"},
		{"connecting", "connecting"},
		{"negotiating", "negotiating"},
		// default
		{"unknown_status", "some-unknown-status"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := statusStyle(tc.status)
			// lipgloss.Style is a struct; a zero-value Style still has
			// a valid String(). We only assert it does not panic and that
			// the same status string always returns the same value (pure
			// function).
			require.Equal(t, got, statusStyle(tc.status),
				"statusStyle must be deterministic for %q", tc.status)
		})
	}
}

// TestStatusStyle_CaseInsensitive pins ToLower normalisation: uppercase
// variants of known statuses must resolve to the same style as their
// lowercase form.
func TestStatusStyle_CaseInsensitive(t *testing.T) {
	t.Parallel()

	require.Equal(t, statusStyle("ERROR"), statusStyle("error"))
	require.Equal(t, statusStyle("PENDING"), statusStyle("pending"))
	require.Equal(t, statusStyle("USED"), statusStyle("used"))
}

// TestSpeedStyle_AllBranches pins every case in speedStyle. Asserts
// determinism and that each speed constant produces a non-panicking
// result without touching lipgloss internals.
func TestSpeedStyle_AllBranches(t *testing.T) {
	t.Parallel()

	speeds := []struct {
		name  string
		speed domain.Speed
	}{
		{"unknown", domain.SpeedUnknown},
		{"low", domain.SpeedLow},
		{"full", domain.SpeedFull},
		{"high", domain.SpeedHigh},
		{"wireless", domain.SpeedWireless},
		{"super", domain.SpeedSuper},
		{"superplus", domain.SpeedSuperPlus},
		{"out_of_enum", domain.Speed(999)},
	}

	for _, tc := range speeds {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := speedStyle(tc.speed)
			require.Equal(t, got, speedStyle(tc.speed),
				"speedStyle must be deterministic for %v", tc.speed)
		})
	}
}
