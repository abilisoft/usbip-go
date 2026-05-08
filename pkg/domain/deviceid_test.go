package domain_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestDeviceID_BusDevNum(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		busnum uint16
		devnum uint16
	}{
		{"zero", 0, 0},
		{"one_one", 1, 1},
		{"bus_max", 0xFFFF, 0},
		{"dev_max", 0, 0xFFFF},
		{"both_max", 0xFFFF, 0xFFFF},
		{"mixed", 0x1234, 0x5678},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id := domain.DeviceID((uint32(tc.busnum) << 16) | uint32(tc.devnum))
			require.Equal(t, tc.busnum, id.BusNum())
			require.Equal(t, tc.devnum, id.DevNum())
		})
	}
}
