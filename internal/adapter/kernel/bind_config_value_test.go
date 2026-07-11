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
		testFSModuleUSBIPCorePath:                               &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleUSBIPHostPath:                               &fstest.MapFile{Mode: fs.ModeDir},
		testFSUSBIPHostDir:                                      &fstest.MapFile{Mode: fs.ModeDir},
		testFSUSBIPHostMatchBusIDPath:                           &fstest.MapFile{Data: []byte("")},
		testFSUSBIPHostBindPath:                                 &fstest.MapFile{Data: []byte("")},
		testFSUSBIPHostUnbindPath:                               &fstest.MapFile{Data: []byte("")},
		testFSUSBIPHostRebindPath:                               &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + busID:                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue": &fstest.MapFile{Data: []byte(fmtInt(configValue) + "\n")},
		"sys/bus/usb/devices/" + iface:                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + iface + "/driver/driver_name":  &fstest.MapFile{Data: []byte("cdc_ether\n")},
		"sys/bus/usb/devices/" + iface + "/driver":              &fstest.MapFile{Data: []byte("cdc_ether\n")},
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

// TestBind_HighConfigurationValue_ProceedsViaBareDeviceUnbind pins
// that Bind succeeds for devices whose active configuration is not 1
// (e.g. ASIX AX88179 USB-Ethernet adapter using config 2 with
// interfaces 3-1:2.0 and 3-1:2.1). Upstream usbip_bind.c uses only
// bare-device unbind and lets USB core's generic disconnect cascade
// to interfaces — so a non-1 configValue is irrelevant. Without
// reading bConfigurationValue at all, this test verifies that we
// don't regress to the original ":1.0" hardcode by confirming a
// configValue=2 device still binds with the same three-write
// sequence.
func TestBind_HighConfigurationValue_ProceedsViaBareDeviceUnbind(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("3-1")

	mfs := bindFSWithConfig(string(busID), 2)
	// Bare-device driver = testUeventSubsystemUSB (kernel default). Bind must unbind
	// it as the FIRST write, not poke the interface at all.
	mfs["sys/bus/usb/devices/"+string(busID)+"/driver/driver_name"] = &fstest.MapFile{Data: []byte("usb\n")}
	mfs["sys/bus/usb/devices/"+string(busID)+"/driver"] = &fstest.MapFile{Data: []byte("usb\n")}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.NoError(t, err,
		"Bind must succeed against a device whose active configuration is 2")

	require.Len(t, rec.calls, 3,
		"Bind sequence: match_busid add → bare-device unbind → usbip-host bind")

	// match_busid add MUST be first — populating the table before the
	// unbind cascade triggers kernel auto-probe ensures usbip-host's
	// stub_probe wins the race against the original interface driver.
	require.Equal(t, testUSBIPHostMatchBusIDPath, rec.calls[0].Path,
		"first write must populate match_busid before any unbind")
	require.Equal(t, "add "+string(busID), rec.calls[0].Data,
		"match_busid takes the 'add <busid>' command form")

	// Bare-device unbind second — NOT cdc_ether/<iface>.
	require.Equal(t, "/sys/bus/usb/drivers/usb/unbind", rec.calls[1].Path,
		"second write must target the bare-device driver, not cdc_ether/<iface>")
	require.Equal(t, string(busID), rec.calls[1].Data,
		"bare-device unbind takes the BARE busid, not iface")

	// Final write: bind to usbip-host with the BARE busid.
	require.Equal(t, testUSBIPHostBindPath, rec.calls[2].Path)
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

	mfs := bindFSWithConfig(string(busID), 2)
	// Unbind precheck requires bare driver == usbip-host. Mirrors the
	// post-Bind kernel state.
	mfs["sys/bus/usb/devices/"+string(busID)+"/driver/driver_name"] = &fstest.MapFile{Data: []byte("usbip-host\n")}
	mfs["sys/bus/usb/devices/"+string(busID)+"/driver"] = &fstest.MapFile{Data: []byte("usbip-host\n")}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), busID)
	require.NoError(t, err)

	require.Len(t, rec.calls, 4,
		"Unbind sequence is sockfd disconnect → unbind → match_busid del → rebind, four writes total")

	// rec.calls[0]: sockfd pre-disconnect (verified separately).
	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/unbind", rec.calls[1].Path)
	require.Equal(t, string(busID), rec.calls[1].Data,
		"usbip-host unbind takes BARE busid; iface would mismatch dev->driver in unbind_store and surface ENODEV")
}
