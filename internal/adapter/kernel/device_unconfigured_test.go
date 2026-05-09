// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestListLocalDevices_UnconfiguredDevice_StillListed pins the
// readByteAttr empty-string contract: when the kernel emits an empty
// value for byte sysfs attributes derived from the active configuration
// (bConfigurationValue, bNumInterfaces, bNumConfigurations) — the
// drivers/usb/core/config.c::show_bConfigurationValue path that runs
// when actconfig is NULL — readByteAttr MUST treat the empty string as
// 0 rather than failing ParseUint and dropping the device from the
// list.
//
// This is the post-bind state of a device whose new driver (e.g.
// usbip-host's stub_dev) has not yet picked a configuration: bound,
// enumerated, but unconfigured. Without this branch ListLocalDevices
// silently skipped any such device with a Warn log and the operator's
// `usbip-go list -l` returned an empty table — defeating the whole
// "did my bind take effect" diagnostic.
func TestListLocalDevices_UnconfiguredDevice_StillListed(t *testing.T) {
	t.Parallel()

	const busID = "3-1"

	mfs := fstest.MapFS{
		"sys/module/usbip_core":                             &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                             &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID:                      &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/idVendor":        &fstest.MapFile{Data: []byte("0b95\n")},
		"sys/bus/usb/devices/" + busID + "/idProduct":       &fstest.MapFile{Data: []byte("1790\n")},
		"sys/bus/usb/devices/" + busID + "/bcdDevice":       &fstest.MapFile{Data: []byte("0210\n")},
		"sys/bus/usb/devices/" + busID + "/busnum":          &fstest.MapFile{Data: []byte("3\n")},
		"sys/bus/usb/devices/" + busID + "/devnum":          &fstest.MapFile{Data: []byte("2\n")},
		"sys/bus/usb/devices/" + busID + "/speed":           &fstest.MapFile{Data: []byte("5000\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceClass":    &fstest.MapFile{Data: []byte("00\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceSubClass": &fstest.MapFile{Data: []byte("00\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceProtocol": &fstest.MapFile{Data: []byte("00\n")},
		// EMPTY values — kernel's actconfig==NULL post-bind state.
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue": &fstest.MapFile{Data: []byte("\n")},
		"sys/bus/usb/devices/" + busID + "/bNumConfigurations":  &fstest.MapFile{Data: []byte("\n")},
		"sys/bus/usb/devices/" + busID + "/bNumInterfaces":      &fstest.MapFile{Data: []byte("\n")},
	}

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err,
		"empty byte-attr values must not fail ListLocalDevices — they encode an "+
			"unconfigured device which is a normal post-bind state")
	require.Len(t, got, 1,
		"unconfigured device MUST appear in the local list so operators can "+
			"verify their bind took effect via `usbip-go list -l`")
	require.Equal(t, domain.BusID(busID), got[0].BusID)
	require.Equal(t, uint8(0), got[0].ConfigValue,
		"empty bConfigurationValue must read as 0")
	require.Equal(t, uint8(0), got[0].NumConfigs,
		"empty bNumConfigurations must read as 0 — the same actconfig==NULL "+
			"sysfs path applies to every byte attr derived from the active config")
	require.Equal(t, uint8(0), got[0].NumInterfaces,
		"empty bNumInterfaces must read as 0")
	require.Empty(t, got[0].Interfaces,
		"unconfigured device exposes no interface descriptors; readInterfaces "+
			"with count=0 must short-circuit cleanly")
}

// TestListLocalDevices_NonNumericByteAttr_ReportsParseError pins the
// readByteAttr non-empty parse-failure contract: when the kernel sysfs
// path emits a non-numeric, non-empty value (corruption, FUSE-style
// overlay, future kernel format change) readByteAttr MUST return a
// wrapped parse error rather than silently coercing to 0. The empty
// branch handles the *expected* unconfigured case; this branch
// surfaces the *unexpected* malformed case so operators can diagnose.
func TestListLocalDevices_NonNumericByteAttr_ReportsParseError(t *testing.T) {
	t.Parallel()

	const busID = "3-1"

	mfs := fstest.MapFS{
		"sys/module/usbip_core":                                 &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                                 &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID:                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/idVendor":            &fstest.MapFile{Data: []byte("0b95\n")},
		"sys/bus/usb/devices/" + busID + "/idProduct":           &fstest.MapFile{Data: []byte("1790\n")},
		"sys/bus/usb/devices/" + busID + "/bcdDevice":           &fstest.MapFile{Data: []byte("0210\n")},
		"sys/bus/usb/devices/" + busID + "/busnum":              &fstest.MapFile{Data: []byte("3\n")},
		"sys/bus/usb/devices/" + busID + "/devnum":              &fstest.MapFile{Data: []byte("2\n")},
		"sys/bus/usb/devices/" + busID + "/speed":               &fstest.MapFile{Data: []byte("5000\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceClass":        &fstest.MapFile{Data: []byte("00\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceSubClass":     &fstest.MapFile{Data: []byte("00\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceProtocol":     &fstest.MapFile{Data: []byte("00\n")},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue": &fstest.MapFile{Data: []byte("not-a-number\n")},
		"sys/bus/usb/devices/" + busID + "/bNumConfigurations":  &fstest.MapFile{Data: []byte("1\n")},
		"sys/bus/usb/devices/" + busID + "/bNumInterfaces":      &fstest.MapFile{Data: []byte("1\n")},
	}

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	// ListLocalDevices skips devices whose readDeviceCore errors and
	// returns the surviving rows; the malformed device is dropped from
	// the listing rather than failing the whole call. Either zero
	// rows or a non-zero count without our busID is acceptable — what
	// MUST hold is that readByteAttr's parse-error branch fires
	// (covered via the coverage profile), not the empty short-circuit.
	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err,
		"a single malformed device must not collapse the whole listing")

	for _, d := range got {
		require.NotEqual(t, domain.BusID(busID), d.BusID,
			"the malformed device must be dropped from the result, not silently zeroed")
	}
}
