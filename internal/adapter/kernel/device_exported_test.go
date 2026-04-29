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

// listExportedFS builds a sysfs fixture with three devices in
// distinct states:
//
//  1. busID="1-1": bound to usbip-host, usbip_status=1 (available) → EXPORTED
//  2. busID="2-1": bound to cdc_ether (native) → NOT exported
//  3. busID="3-1": bound to usbip-host, usbip_status=2 (USED) → NOT exported
//
// Mirrors what upstream usbipd's send_reply_devlist filters: devices
// must be on usbip-host AND not currently claimed by an importer.
func listExportedFS() fstest.MapFS {
	mfs := fstest.MapFS{
		"sys/module/usbip_core":                      &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                      &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host":             &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host/match_busid": &fstest.MapFile{Data: []byte("")},
	}

	for _, b := range []struct {
		busID  string
		driver string
		status string
	}{
		{"1-1", "usbip-host", "1"},
		{"2-1", "cdc_ether", ""},
		{"3-1", "usbip-host", "2"},
	} {
		base := "sys/bus/usb/devices/" + b.busID

		mfs[base] = &fstest.MapFile{Mode: fs.ModeDir}
		mfs[base+"/idVendor"] = &fstest.MapFile{Data: []byte("0951\n")}
		mfs[base+"/idProduct"] = &fstest.MapFile{Data: []byte("1666\n")}
		mfs[base+"/bcdDevice"] = &fstest.MapFile{Data: []byte("0100\n")}
		mfs[base+"/busnum"] = &fstest.MapFile{Data: []byte("1\n")}
		mfs[base+"/devnum"] = &fstest.MapFile{Data: []byte("2\n")}
		mfs[base+"/speed"] = &fstest.MapFile{Data: []byte("480\n")}
		mfs[base+"/bDeviceClass"] = &fstest.MapFile{Data: []byte("00\n")}
		mfs[base+"/bDeviceSubClass"] = &fstest.MapFile{Data: []byte("00\n")}
		mfs[base+"/bDeviceProtocol"] = &fstest.MapFile{Data: []byte("00\n")}
		mfs[base+"/bConfigurationValue"] = &fstest.MapFile{Data: []byte("1\n")}
		mfs[base+"/bNumConfigurations"] = &fstest.MapFile{Data: []byte("1\n")}
		mfs[base+"/bNumInterfaces"] = &fstest.MapFile{Data: []byte("1\n")}
		mfs[base+"/driver/driver_name"] = &fstest.MapFile{Data: []byte(b.driver + "\n")}
		mfs[base+"/driver"] = &fstest.MapFile{Data: []byte(b.driver + "\n")}
		mfs[base+"/manufacturer"] = &fstest.MapFile{Data: []byte("ASIX\n")}
		mfs[base+"/product"] = &fstest.MapFile{Data: []byte("AX88179\n")}

		if b.status != "" {
			mfs[base+"/usbip_status"] = &fstest.MapFile{Data: []byte(b.status + "\n")}
		}
	}

	return mfs
}

// TestListExportedDevices_FilterByDriverAndStatus pins the
// remote-devlist filter contract: ListExportedDevices returns ONLY
// devices bound to usbip-host that are not already claimed by an
// importer. Without this filter peers see the entire local USB bus
// (unbound HID devices, USB ethernet adapters bound to cdc_ether,
// devices already attached by another importer) and waste round trips
// on un-importable busids.
func TestListExportedDevices_FilterByDriverAndStatus(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewExporterAdapter(kernel.WithFS(listExportedFS()))
	require.NoError(t, err)

	devs, err := a.ListExportedDevices(context.Background())
	require.NoError(t, err)

	require.Len(t, devs, 1,
		"only the usbip-host bound + not-USED device should be reported")
	require.Equal(t, domain.BusID("1-1"), devs[0].BusID,
		"1-1 is bound to usbip-host with status=1 (available)")
}

// TestListLocalDevices_StillReturnsEverything pins that the CLI's
// local view is unchanged: ListLocalDevices reports every USB device
// regardless of bind state, since `usbip-go list -l` shows the whole
// bus to operators.
func TestListLocalDevices_StillReturnsEverything(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewExporterAdapter(kernel.WithFS(listExportedFS()))
	require.NoError(t, err)

	devs, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)

	require.Len(t, devs, 3,
		"ListLocalDevices must continue to report every USB device, including unbound and USED ones")
}
