// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_DeactivatesNetdev pins the actual netdev preflight on
// fixture data shaped like real Linux sysfs:
//
//   - /sys/bus/usb/devices/<busid>/net/<name> directory exists for
//     network adapters (cdc_ether, r8152, …)
//   - /sys/class/net/<name>/flags is a HEX string (e.g. "0x1003")
//     with IFF_UP=0x1 set
//
// The preflight must read the hex value, clear IFF_UP, write back
// the masked value. Without this test the prior implementation
// silently failed (decimal parser on hex input) yet still passed
// CI because the only existing fixture had no net subtree at all.
func TestBind_DeactivatesNetdev(t *testing.T) {
	t.Parallel()

	const (
		busID   = "1-1"
		netName = "enxAA00"
	)

	mfs := fstest.MapFS{
		"sys/module/usbip_core":                                    &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                                    &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host":                           &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host/match_busid":               &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/bind":                      &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + busID:                             &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue":    &fstest.MapFile{Data: []byte("1\n")},
		"sys/bus/usb/devices/" + busID + ":1.0":                    &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + ":1.0/driver/driver_name": &fstest.MapFile{Data: []byte("cdc_ether\n")},
		// Network sysfs hooks: the netdev directory under the device,
		// and the corresponding /sys/class/net/<name>/flags file with
		// IFF_UP set.
		"sys/bus/usb/devices/" + busID + "/net":            &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/net/" + netName: &fstest.MapFile{Mode: fs.ModeDir},
		"sys/class/net/" + netName:                         &fstest.MapFile{Mode: fs.ModeDir},
		"sys/class/net/" + netName + "/flags":              &fstest.MapFile{Data: []byte("0x1003\n")},
	}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.NoError(t, err)

	// First write must clear IFF_UP on the netdev. Expected new
	// flags = 0x1003 &^ 0x0001 = 0x1002.
	require.NotEmpty(t, rec.calls)
	require.Equal(t, "/sys/class/net/"+netName+"/flags", rec.calls[0].Path,
		"deactivateNetdevs must write to /sys/class/net/<name>/flags before the driver unbind")

	stripped := strings.TrimPrefix(strings.TrimPrefix(rec.calls[0].Data, "0x"), "0X")
	require.Equal(t, "1002", strings.TrimSpace(stripped),
		"new flags value must clear IFF_UP (bit 0x1) — 0x1003 -> 0x1002; got: %q", rec.calls[0].Data)
}
