// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestParseBusID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want domain.BusID
		err  bool
	}{
		{"simple", testRootBusID, domain.BusID(testRootBusID), false},
		{"nested", testNestedBusID, domain.BusID(testNestedBusID), false},
		{"nested_deeper", "2-3.4.5.6", domain.BusID("2-3.4.5.6"), false},
		{"max_length_digits", "1-" + strings.Repeat("1", 29), domain.BusID("1-" + strings.Repeat("1", 29)), false},
		{"empty", "", "", true},
		{"whitespace", " ", "", true},
		{"over_limit", "1-" + strings.Repeat("1", 31), "", true},
		{"contains_null", "1-\x00", "", true},
		// Malformed topology per v1 contract §4.1 (pattern must be ^\d+-[\d\.]+$).
		{"no_dash", "abc", "", true},
		{"trailing_dash", "1-", "", true},
		{"leading_dash", "-1", "", true},
		{"double_dash", "1--2", "", true},
		{"letters", "1-a", "", true},
		{"space_in_middle", "1- 1", "", true},
		{"trailing_dot", "1-1.", "", true},
		{"double_dot", "1-1..2", "", true},
		{"leading_zero_prefix_space", " 1-1", "", true},
		{"trailing_space", "1-1 ", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.ParseBusID(tc.in)
			if tc.err {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.True(t, got.IsValid())
			require.Equal(t, string(tc.want), got.String())
		})
	}
}

func TestBusID_IsValid_ZeroValue(t *testing.T) {
	t.Parallel()

	var b domain.BusID
	require.False(t, b.IsValid())
}

func TestBusID_IsValid_Branches(t *testing.T) {
	t.Parallel()

	// Constructed directly (bypassing ParseBusID) to exercise each guard.
	require.False(t, domain.BusID(strings.Repeat("a", 32)).IsValid())
	require.False(t, domain.BusID("a\x00b").IsValid())
	require.False(t, domain.BusID("   ").IsValid())
	require.True(t, domain.BusID(testRootBusID).IsValid())
}
