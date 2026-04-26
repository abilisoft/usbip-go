// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestReadSpeedAttr pins the sysfs Mbps string → domain.Speed mapping.
// Linux's drivers/usb/core/sysfs.c speed_show() emits Mbps strings
// ("5000", not "5"), so a raw ReadUint + cast produces wrong enum values
// for SuperSpeed and SuperSpeed+ devices and a parse error for Low Speed.
func TestReadSpeedAttr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		want    domain.Speed
	}{
		{"low_speed_1.5_mbps", "1.5\n", domain.SpeedLow},
		{"full_speed_12_mbps", "12\n", domain.SpeedFull},
		{"high_speed_480_mbps", "480\n", domain.SpeedHigh},
		{"super_speed_5000_mbps", "5000\n", domain.SpeedSuper},
		{"super_speed_plus_10000_mbps", "10000\n", domain.SpeedSuperPlus},
		{"super_speed_plus_20000_mbps_usb32_gen2x2", "20000\n", domain.SpeedSuperPlus},
		{"unknown_pre_enumeration", "unknown\n", domain.SpeedUnknown},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fsys := fstest.MapFS{
				"sys/bus/usb/devices/1-1/speed": &fstest.MapFile{Data: []byte(tc.content)},
			}

			got, err := kernel.ReadSpeedAttr(fsys, "sys/bus/usb/devices/1-1/speed")
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestReadSpeedAttrRejectsUnrecognized(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
	}{
		{"raw_enum_value_5", "5\n"},
		{"raw_enum_value_3", "3\n"},
		{"garbage", "not-a-speed\n"},
		{"partial_match", "500\n"},
		{"empty", "\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fsys := fstest.MapFS{
				"sys/bus/usb/devices/1-1/speed": &fstest.MapFile{Data: []byte(tc.content)},
			}

			_, err := kernel.ReadSpeedAttr(fsys, "sys/bus/usb/devices/1-1/speed")
			require.Error(t, err, "must reject unrecognized sysfs speed %q", tc.content)
			require.Contains(t, err.Error(), "unrecognized sysfs speed",
				"error must identify the speed-table miss, not a different read failure")
		})
	}
}
