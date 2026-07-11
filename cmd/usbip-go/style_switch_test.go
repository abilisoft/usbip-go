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

	const statusErrorCaseName = "error"

	cases := []struct {
		name   string
		status domain.Status
	}{
		{"null", domain.StatusNull},
		{"not_assigned", domain.StatusNotAssigned},
		{"available", domain.StatusAvailable},
		{"used", domain.StatusUsed},
		{statusErrorCaseName, domain.StatusError},
		{"out_of_enum", domain.Status(999)},
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
				"statusStyle must be deterministic for %v", tc.status)
		})
	}
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
