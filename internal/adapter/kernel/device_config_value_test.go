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

// TestListLocalDevices_EnumeratesInterfacesAtActiveConfig pins that
// readInterfaces uses the device's currently-active configuration
// (bConfigurationValue=2 here) instead of the historical hardcoded
// config 1. Without this, devices with config != 1 surface as
// domain.Device with an empty Interfaces slice — the same regression
// class as the speed-string and bind hardcoded ":1.0" cases.
func TestListLocalDevices_EnumeratesInterfacesAtActiveConfig(t *testing.T) {
	t.Parallel()

	const (
		busID  = "3-1"
		config = 2
	)

	ifaceName := busID + ":2.0"

	mfs := fstest.MapFS{
		"sys/module/usbip_core":                                 &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                                 &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID:                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/idVendor":            &fstest.MapFile{Data: []byte("0x0951\n")},
		"sys/bus/usb/devices/" + busID + "/idProduct":           &fstest.MapFile{Data: []byte("0x1666\n")},
		"sys/bus/usb/devices/" + busID + "/bcdDevice":           &fstest.MapFile{Data: []byte("1100\n")},
		"sys/bus/usb/devices/" + busID + "/busnum":              &fstest.MapFile{Data: []byte("3\n")},
		"sys/bus/usb/devices/" + busID + "/devnum":              &fstest.MapFile{Data: []byte("7\n")},
		"sys/bus/usb/devices/" + busID + "/speed":               &fstest.MapFile{Data: []byte("480\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceClass":        &fstest.MapFile{Data: []byte("00\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceSubClass":     &fstest.MapFile{Data: []byte("00\n")},
		"sys/bus/usb/devices/" + busID + "/bDeviceProtocol":     &fstest.MapFile{Data: []byte("00\n")},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue": &fstest.MapFile{Data: []byte("2\n")},
		"sys/bus/usb/devices/" + busID + "/bNumConfigurations":  &fstest.MapFile{Data: []byte("3\n")},
		"sys/bus/usb/devices/" + busID + "/bNumInterfaces":      &fstest.MapFile{Data: []byte("1\n")},
		// Interface lives at <busid>:2.0, not :1.0 — exercise that
		// readInterfaces uses configValue=2.
		"sys/bus/usb/devices/" + ifaceName:                         &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + ifaceName + "/bInterfaceClass":    &fstest.MapFile{Data: []byte("ff\n")},
		"sys/bus/usb/devices/" + ifaceName + "/bInterfaceSubClass": &fstest.MapFile{Data: []byte("aa\n")},
		"sys/bus/usb/devices/" + ifaceName + "/bInterfaceProtocol": &fstest.MapFile{Data: []byte("01\n")},
		"sys/bus/usb/devices/" + ifaceName + "/bAlternateSetting":  &fstest.MapFile{Data: []byte("0\n")},
	}

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, domain.BusID(busID), got[0].BusID)
	require.Equal(t, uint8(config), got[0].ConfigValue,
		"core ConfigValue must reflect the currently-active config from sysfs")
	require.Len(t, got[0].Interfaces, 1,
		"interface enumeration must walk <busid>:<configValue>.<n>, not :1.<n>")
	require.Equal(t, domain.USBClass(0xFF), got[0].Interfaces[0].Class,
		"interface descriptor must be read from the config-2 path, not a phantom config-1 fallback")
	require.Equal(t, domain.USBSubclass(0xAA), got[0].Interfaces[0].Subclass)
}
