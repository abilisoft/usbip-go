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

// bindFSWithConfig builds a sysfs fixture for a device whose currently
// active configuration is configValue (not necessarily 1). Mirrors what
// real Linux exposes for devices that select a higher-numbered config:
// the interface directories live at "<busid>:<configValue>.<n>", not
// at ":1.<n>". Reproduces the layout the user encountered with a
// USB-to-Ethernet adapter exposing 3-1:2.0 and 3-1:2.1.
func bindFSWithConfig(busID string, configValue int) fstest.MapFS {
	iface := fmtIface(busID, configValue, 0)

	return fstest.MapFS{
		"sys/module/usbip_core":                                &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                                &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host":                       &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host/match_busid":           &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/bind":                  &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/unbind":                &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/rebind":                &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + busID:                         &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue": &fstest.MapFile{Data: []byte(fmtInt(configValue) + "\n")},
		"sys/bus/usb/devices/" + iface:                         &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + iface + "/driver/driver_name": &fstest.MapFile{Data: []byte("cdc_ether\n")},
		"sys/bus/usb/devices/" + iface + "/driver":             &fstest.MapFile{Data: []byte("cdc_ether\n")},
	}
}

func fmtIface(busID string, configValue, ifaceIdx int) string {
	return busID + ":" + fmtInt(configValue) + "." + fmtInt(ifaceIdx)
}

func fmtInt(n int) string {
	if n == 0 {
		return "0"
	}

	var buf []byte

	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)

		n /= 10
	}

	return string(buf)
}

// TestBind_RespectsBConfigurationValue pins that Bind reads the
// device's currently active configuration from sysfs and constructs
// the interface name accordingly, instead of hardcoding ":1.0".
//
// Reproduces the operator-visible failure: a device whose default
// configuration is 2 has no "<busid>:1.0" interface in sysfs at all.
// A hardcoded ":1.0" makes Bind ENOENT on the driver_name read and
// surface ErrDeviceNotFound, even though the device IS present and
// bindable on its actual interface.
//
// Reference: upstream libsrc/usbip_host_driver.c uses
//   sprintf(busid, "%s:%d.0", dev->busid, dev->bConfigurationValue);
func TestBind_RespectsBConfigurationValue(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("3-1")
	wantIface := fmtIface(string(busID), 2, 0) // 3-1:2.0
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFSWithConfig(string(busID), 2)),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.NoError(t, err,
		"Bind must succeed against a device whose active configuration is 2")

	require.Len(t, rec.calls, 3,
		"Bind sequence is unbind → match_busid → bind, three writes total")

	// First write: unbind from the actual driver, with the actual iface.
	require.Equal(t, "/sys/bus/usb/drivers/cdc_ether/unbind", rec.calls[0].Path)
	require.Equal(t, wantIface, rec.calls[0].Data,
		"unbind must target the iface derived from bConfigurationValue, not :1.0")

	// Third write: bind to usbip-host with the BARE busid because
	// usbip-host is a usb_device_driver. This was the deeper bug
	// hidden by the original ":1.0" hardcode.
	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/bind", rec.calls[2].Path)
	require.Equal(t, string(busID), rec.calls[2].Data,
		"usbip-host is a usb_device_driver — its bind sysfs accepts BARE busid, never iface")
}

// TestUnbind_UsesBareBusidNotIface pins that Unbind writes the bare
// busid (not iface) to usbip-host/unbind. The kernel's driver unbind
// handler looks the device up by name on the bus and only proceeds
// when dev->driver matches; usbip-host (usb_device_driver) is bound
// to the usb_device, so the lookup must be the device name, not the
// interface name.
func TestUnbind_UsesBareBusidNotIface(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("3-1")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFSWithConfig(string(busID), 2)),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), busID)
	require.NoError(t, err)

	require.Len(t, rec.calls, 3,
		"Unbind sequence is unbind → match_busid del → rebind, three writes total")

	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/unbind", rec.calls[0].Path)
	require.Equal(t, string(busID), rec.calls[0].Data,
		"usbip-host unbind takes BARE busid; iface would mismatch dev->driver in unbind_store and surface ENODEV")
}
