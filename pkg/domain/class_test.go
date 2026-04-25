// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestUSBClass_String(t *testing.T) {
	t.Parallel()

	// Every entry in the committed usb.ids subset is exercised so the
	// jump-table switch in class_data.go has full branch coverage.
	cases := []struct {
		name string
		in   domain.USBClass
		want string
	}{
		{"use_interface_descriptor", domain.USBClass(0x00), "use-interface-descriptor"},
		{"audio", domain.USBClass(0x01), "audio"},
		{"communications", domain.USBClass(0x02), "communications"},
		{"hid", domain.USBClass(0x03), "hid"},
		{"physical", domain.USBClass(0x05), "physical"},
		{"image", domain.USBClass(0x06), "image"},
		{"printer", domain.USBClass(0x07), "printer"},
		{"mass_storage", domain.USBClass(0x08), "mass-storage"},
		{"hub", domain.USBClass(0x09), "hub"},
		{"cdc_data", domain.USBClass(0x0A), "cdc-data"},
		{"smart_card", domain.USBClass(0x0B), "smart-card"},
		{"content_security", domain.USBClass(0x0D), "content-security"},
		{"video", domain.USBClass(0x0E), "video"},
		{"healthcare", domain.USBClass(0x0F), "personal-healthcare"},
		{"audio_video", domain.USBClass(0x10), "audio-video"},
		{"billboard", domain.USBClass(0x11), "billboard"},
		{"typec_bridge", domain.USBClass(0x12), "usb-type-c-bridge"},
		{"diagnostic", domain.USBClass(0xDC), "diagnostic"},
		{"wireless", domain.USBClass(0xE0), "wireless"},
		{"miscellaneous", domain.USBClass(0xEF), "miscellaneous"},
		{"application_specific", domain.USBClass(0xFE), "application-specific"},
		{"vendor_specific", domain.USBClass(0xFF), "vendor-specific"},
		{"unknown_mid_gap", domain.USBClass(0x42), "class(0x42)"},
		{"unknown_reserved", domain.USBClass(0x04), "class(0x04)"},
		{"unknown_0c_reserved", domain.USBClass(0x0C), "class(0x0c)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.in.String())
		})
	}
}

func TestUSBSubclass_String(t *testing.T) {
	t.Parallel()

	// Subclass numbers are only meaningful within a class, but the typedef
	// itself is just a uint8 with a hex-formatted fallback.
	require.Equal(t, "subclass(0x06)", domain.USBSubclass(0x06).String())
	require.Equal(t, "subclass(0x00)", domain.USBSubclass(0x00).String())
	require.Equal(t, "subclass(0xff)", domain.USBSubclass(0xFF).String())
}

func TestUSBProtocol_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "protocol(0x01)", domain.USBProtocol(0x01).String())
	require.Equal(t, "protocol(0x00)", domain.USBProtocol(0x00).String())
	require.Equal(t, "protocol(0xff)", domain.USBProtocol(0xFF).String())
}
