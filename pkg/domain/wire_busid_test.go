// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
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
		{"topology shape", "1-1.2", "1-1.2", false},
		{"vudc shape", "usbip-vudc.0", "usbip-vudc.0", false},
		{"bus root", "1-1", "1-1", false},
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
