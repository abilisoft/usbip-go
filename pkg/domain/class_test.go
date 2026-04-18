package domain_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestUSBClass_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   domain.USBClass
		want string
	}{
		{"hid", domain.USBClass(0x03), "hid"},
		{"mass_storage", domain.USBClass(0x08), "mass-storage"},
		{"hub", domain.USBClass(0x09), "hub"},
		{"vendor", domain.USBClass(0xFF), "vendor-specific"},
		{"unknown", domain.USBClass(0x42), "class(0x42)"},
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
