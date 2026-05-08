// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestSpeed_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   domain.Speed
		want string
	}{
		{"unknown_is_zero", domain.SpeedUnknown, "unknown"},
		{"low", domain.SpeedLow, "Low-Speed (1.5Mbps)"},
		{"full", domain.SpeedFull, "Full-Speed (12Mbps)"},
		{"high", domain.SpeedHigh, "High-Speed (480Mbps)"},
		{"wireless", domain.SpeedWireless, "Wireless"},
		{"super", domain.SpeedSuper, "SuperSpeed (5Gbps)"},
		{"superplus", domain.SpeedSuperPlus, "SuperSpeed+ (10/20Gbps)"},
		{"fallback", domain.Speed(42), "speed(42)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.in.String())
		})
	}
}

func TestSpeed_NumericValues(t *testing.T) {
	t.Parallel()

	// Match kernel enum usb_device_speed order.
	require.Equal(t, domain.SpeedUnknown, domain.Speed(0))
	require.Equal(t, domain.SpeedLow, domain.Speed(1))
	require.Equal(t, domain.SpeedFull, domain.Speed(2))
	require.Equal(t, domain.SpeedHigh, domain.Speed(3))
	require.Equal(t, domain.SpeedWireless, domain.Speed(4))
	require.Equal(t, domain.SpeedSuper, domain.Speed(5))
	require.Equal(t, domain.SpeedSuperPlus, domain.Speed(6))
}
