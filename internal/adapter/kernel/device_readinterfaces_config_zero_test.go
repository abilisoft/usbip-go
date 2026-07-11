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

// TestListLocalDevices_ConfigValueZeroFallsBackToConfig1 pins the
// readInterfaces fallback branch: when sysfs reports bConfigurationValue=0
// (device not yet configured by the host), the interface enumeration must
// fall back to defaultConfigIndex=1 and look for "<busid>:1.<n>" entries.
// Without this, the code would look for "<busid>:0.<n>" which the kernel
// never creates, and the device would surface with an empty Interfaces
// slice — silently hiding the attached interface from callers.
func TestListLocalDevices_ConfigValueZeroFallsBackToConfig1(t *testing.T) {
	t.Parallel()

	const busID = "4-1"

	// Interface at :1.0 — the fallback config-1 path that readInterfaces
	// must resolve when bConfigurationValue=0.
	ifaceName := busID + ":1.0"

	mfs := fstest.MapFS{
		testFSModuleUSBIPCorePath:                                  &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleUSBIPHostPath:                                  &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID:                             &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/idVendor":               &fstest.MapFile{Data: []byte("0x1234\n")},
		"sys/bus/usb/devices/" + busID + "/idProduct":              &fstest.MapFile{Data: []byte("0x5678\n")},
		"sys/bus/usb/devices/" + busID + "/bcdDevice":              &fstest.MapFile{Data: []byte("0100\n")},
		"sys/bus/usb/devices/" + busID + "/busnum":                 &fstest.MapFile{Data: []byte("4\n")},
		"sys/bus/usb/devices/" + busID + "/devnum":                 &fstest.MapFile{Data: []byte("2\n")},
		"sys/bus/usb/devices/" + busID + "/speed":                  &fstest.MapFile{Data: []byte("12\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceClass":           &fstest.MapFile{Data: []byte(testZeroDeviceClassRaw)},
		"sys/bus/usb/devices/" + busID + "/bDeviceSubClass":        &fstest.MapFile{Data: []byte(testZeroDeviceClassRaw)},
		"sys/bus/usb/devices/" + busID + "/bDeviceProtocol":        &fstest.MapFile{Data: []byte(testZeroDeviceClassRaw)},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue":    &fstest.MapFile{Data: []byte("0\n")},
		"sys/bus/usb/devices/" + busID + "/bNumConfigurations":     &fstest.MapFile{Data: []byte("1\n")},
		"sys/bus/usb/devices/" + busID + "/bNumInterfaces":         &fstest.MapFile{Data: []byte("1\n")},
		"sys/bus/usb/devices/" + ifaceName:                         &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + ifaceName + "/bInterfaceClass":    &fstest.MapFile{Data: []byte("03\n")},
		"sys/bus/usb/devices/" + ifaceName + "/bInterfaceSubClass": &fstest.MapFile{Data: []byte("01\n")},
		"sys/bus/usb/devices/" + ifaceName + "/bInterfaceProtocol": &fstest.MapFile{Data: []byte("02\n")},
		"sys/bus/usb/devices/" + ifaceName + "/bAlternateSetting":  &fstest.MapFile{Data: []byte("0\n")},
	}

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)

	dev := got[0]

	require.Equal(t, domain.BusID(busID), dev.BusID)
	require.Equal(t, uint8(0), dev.ConfigValue,
		"ConfigValue=0 must be preserved as-is in the domain.Device")
	require.Len(t, dev.Interfaces, 1,
		"readInterfaces must fall back to config 1 and find the interface at <busid>:1.0")
	require.Equal(t, domain.USBClass(0x03), dev.Interfaces[0].Class,
		"interface at :1.0 fallback path must be read correctly")
}
