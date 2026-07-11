// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestListLocalDevices_AllRealSysfsSpeedStrings pins ListLocalDevices
// against every Mbps string Linux drivers/usb/core/sysfs.c speed_show()
// can emit. Prior to the speed_test.go ReadSpeedAttr translation a raw
// ReadUint cast surfaced "5000" as domain.Speed(5000) — outside the
// finite enum, rejected by the wire decoder as ErrProtocolError, and
// invisible at the unit-test layer because the existing makeDeviceAttrs
// fixture used "3\n" (a raw enum integer that never appears in real
// sysfs). Each row here represents a real Linux device population the
// upstream usbipd (linux/tools/usb/usbip) must enumerate without
// protocol mismatch.
func TestListLocalDevices_AllRealSysfsSpeedStrings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		sysfs    string
		expected domain.Speed
	}{
		{"low_speed_1.5_mbps_USB1.0", "1.5\n", domain.SpeedLow},
		{"full_speed_12_mbps_USB1.1", "12\n", domain.SpeedFull},
		{"high_speed_480_mbps_USB2.0", testHighSpeedRaw, domain.SpeedHigh},
		{"super_speed_5000_mbps_USB3.0", testSuperSpeedRaw, domain.SpeedSuper},
		{"super_speed_plus_10000_mbps_USB3.1", "10000\n", domain.SpeedSuperPlus},
		{"super_speed_plus_20000_mbps_USB3.2_Gen2x2", "20000\n", domain.SpeedSuperPlus},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			attrs := makeDeviceAttrs()

			attrs["speed"] = tc.sysfs

			mfs := mergeFS(deviceSysfs(testRootBusID, attrs), moduleDirs())

			a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
			require.NoError(t, err)

			got, err := a.ListLocalDevices(context.Background())
			require.NoError(t, err,
				"ListLocalDevices must succeed for sysfs speed %q", tc.sysfs)
			require.Len(t, got, 1)
			require.Equal(t, tc.expected, got[0].Speed,
				"speed enum must be derived from sysfs Mbps string %q", tc.sysfs)
			require.True(t, got[0].Speed.IsKnown(),
				"speed enum must lie within domain.Speed enum so the wire encoder cannot emit ErrProtocolError")
		})
	}
}

// TestListLocalDevices_RejectsUnrecognizedSysfsSpeed pins the failure
// mode for sysfs strings outside speed_show()'s emit set. The walker
// logs a warning and skips the device rather than surfacing a domain
// device with a garbage Speed value — downstream consumers (structured
// logs, CLI rendering, JSON event delivery) never see an
// out-of-enum value.
func TestListLocalDevices_RejectsUnrecognizedSysfsSpeed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		sysfs string
	}{
		{"raw_kernel_enum_value_3", "3\n"},
		{"raw_kernel_enum_value_5", "5\n"},
		{"truncated_500", "500\n"},
		{"garbage", "fast\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			attrs := makeDeviceAttrs()

			attrs["speed"] = tc.sysfs

			mfs := mergeFS(deviceSysfs(testRootBusID, attrs), moduleDirs())

			// Capture the skip warning so we can assert the device was
			// skipped for the speed reason specifically — not some
			// unrelated read failure that would also produce an empty
			// device list and falsely satisfy a require.Empty assertion.
			var logBuf bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

			a, err := kernel.NewExporterAdapter(
				kernel.WithFS(mfs),
				kernel.WithLogger(logger),
			)
			require.NoError(t, err)

			got, err := a.ListLocalDevices(context.Background())
			require.NoError(t, err,
				"ListLocalDevices must not error overall when one device has bad sysfs — it skips that device")
			require.Empty(t, got,
				"device with unrecognized sysfs speed %q must be skipped", tc.sysfs)
			require.Contains(t, logBuf.String(), "skip device with unreadable sysfs attrs",
				"warning must record the skip event")
			require.Contains(t, logBuf.String(), "busid=1-1",
				"warning must name the offending bus id so an operator can locate the device")
			require.Contains(t, logBuf.String(), "unrecognized sysfs speed",
				"warning must name the speed-table miss as the cause, not a different attr read")
		})
	}
}
