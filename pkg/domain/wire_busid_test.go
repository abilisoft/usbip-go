// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestValidateWireBusID locks in the wire-side decoder's relaxed
// busid acceptance: ParseBusID enforces the strict topology shape
// for caller-supplied input, but ValidateWireBusID accepts any
// busid the kernel could legitimately emit including the
// `usbip-vudc.0` shape used by the vudc test fixtures.
func TestValidateWireBusID(t *testing.T) {
	t.Parallel()

	maxLen := domain.BusIDSize - 1

	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty rejected", "", "", true},
		{"topology shape", testNestedBusID, testNestedBusID, false},
		{"vudc shape", "usbip-vudc.0", "usbip-vudc.0", false},
		{"bus root", testRootBusID, testRootBusID, false},
		{"underscore allowed", "usb_root_2-1", "usb_root_2-1", false},
		{"max length OK", strings.Repeat("a", maxLen), strings.Repeat("a", maxLen), false},
		{"length exceeds cap", strings.Repeat("a", maxLen+1), "", true},
		{"slash rejected", "1-1/2", "", true},
		{"space rejected", "1-1 2", "", true},
		{"control byte rejected", "1-1\x00", "", true},
		{"null byte mid-string", "ab\x00cd", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.ValidateWireBusID(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				require.ErrorIs(t, err, domain.ErrBusIDInvalid)

				return
			}

			require.NoError(t, err)
			require.Equal(t, domain.BusID(tc.want), got)
		})
	}
}

// TestValidateWireBusID_LengthErrorNamesExactCap pins that the
// length-overflow error message names the precise cap (BusIDSize-1).
// Without this an ARITHMETIC_BASE mutation that turns the format-arg
// `BusIDSize-1` into `BusIDSize+1` survives because every other test
// only checks the error type. A wrong cap in the message misleads
// operators chasing a too-long busid down by 2.
func TestValidateWireBusID_LengthErrorNamesExactCap(t *testing.T) {
	t.Parallel()

	maxLen := domain.BusIDSize - 1
	tooLong := strings.Repeat("a", maxLen+1)

	_, err := domain.ValidateWireBusID(tooLong)
	require.Error(t, err)

	wantSuffix := fmt.Sprintf("exceeds %d", maxLen)
	require.Contains(t, err.Error(), wantSuffix,
		"length error must name the canonical max as %q (BusIDSize-1); got: %q", wantSuffix, err.Error())
}

// TestBusIDIsValid_BoundaryLength pins the BusID.IsValid length
// guard at exactly len == BusIDSize. CONDITIONALS_BOUNDARY mutates
// `>=` to `>`; without a boundary input, both treat strings of
// length > BusIDSize as invalid and the mutant survives.
//
// IsValid also enforces a topology shape (busIDPattern), so the
// inputs must be topology-valid otherwise the regex check would
// fail before the length check. Use a "1-1.<digits>" shape and
// pad the trailing digits to land the total length on
// BusIDSize / BusIDSize-1.
func TestBusIDIsValid_BoundaryLength(t *testing.T) {
	t.Parallel()

	// "1-1." is 4 chars; pad with digits to reach the target length.
	const prefix = "1-1."

	atBoundary := prefix + strings.Repeat("9", domain.BusIDSize-len(prefix))
	require.Len(t, atBoundary, domain.BusIDSize, "sanity: at-boundary input length")
	require.False(t, domain.BusID(atBoundary).IsValid(),
		"a busid whose length equals BusIDSize must be invalid; "+
			"CONDITIONALS_BOUNDARY mutant `>` would mistakenly accept it")

	belowBoundary := prefix + strings.Repeat("9", domain.BusIDSize-1-len(prefix))
	require.Len(t, belowBoundary, domain.BusIDSize-1, "sanity: below-boundary input length")
	require.True(t, domain.BusID(belowBoundary).IsValid(),
		"a busid one byte below the cap must remain valid; sanity check that we are not over-rejecting")
}

// TestValidateWireBusID_RuneBoundaries pins the per-rune accept set
// at every boundary char. CONDITIONALS_BOUNDARY mutates each `>=`
// or `<=` in isWireBusIDRune; without boundary chars in the input,
// both real and mutant behave identically for typical inputs and
// the mutants survive.
func TestValidateWireBusID_RuneBoundaries(t *testing.T) {
	t.Parallel()

	// Every char that defines a boundary in isWireBusIDRune. Each
	// MUST be accepted by ValidateWireBusID — a mutation that
	// shifts the boundary by one rejects this exact char.
	accepted := []string{"a", "z", "A", "Z", "0", "9"}
	for _, s := range accepted {
		t.Run("accept_"+s, func(t *testing.T) {
			t.Parallel()

			_, err := domain.ValidateWireBusID(s)
			require.NoError(t, err,
				"boundary rune %q must be accepted; mutation that shifts the boundary one beyond would reject it", s)
		})
	}

	// Every char one beyond the boundary in either direction. Each
	// MUST be rejected — a mutation that broadens the boundary by
	// one accepts this exact char.
	rejected := []string{"`", "{", "@", "[", "/", ":"}
	for _, s := range rejected {
		t.Run("reject_"+s, func(t *testing.T) {
			t.Parallel()

			_, err := domain.ValidateWireBusID(s)
			require.Error(t, err,
				"just-out-of-range rune %q must be rejected; "+
					"a CONDITIONALS_BOUNDARY mutation that broadens the boundary by one would silently accept it", s)
		})
	}
}

// TestParseBusID_LengthErrorNamesExactCap mirrors the above for
// ParseBusID, which has its own copy of the same length guard at
// busid.go:39 and the same ARITHMETIC_BASE mutant survived there
// for the same reason.
//
// Input length is exactly BusIDSize (the smallest length that
// trips `len(s) >= BusIDSize`). A CONDITIONALS_BOUNDARY mutation
// that turns `>=` into `>` must therefore observably let the
// input through, fail the require.Error, and KILL the mutant.
func TestParseBusID_LengthErrorNamesExactCap(t *testing.T) {
	t.Parallel()

	maxLen := domain.BusIDSize - 1
	// Topologically-valid prefix "1-" plus enough digits to land
	// the total length on BusIDSize EXACTLY. "1-" is 2 chars, so
	// we pad with BusIDSize-2 digits.
	tooLong := "1-" + strings.Repeat("1", domain.BusIDSize-2)
	require.Len(t, tooLong, domain.BusIDSize,
		"sanity: input must hit the boundary len == BusIDSize so the >=/> mutation has an observable difference")

	_, err := domain.ParseBusID(tooLong)
	require.Error(t, err)

	wantSuffix := fmt.Sprintf("exceeds %d", maxLen)
	require.Contains(t, err.Error(), wantSuffix,
		"length error must name the canonical max as %q (BusIDSize-1); got: %q", wantSuffix, err.Error())
}
