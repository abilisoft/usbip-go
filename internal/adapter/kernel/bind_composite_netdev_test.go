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

// TestBind_DeactivateNetdevs_CompositeInterfacePath pins the composite-device
// fix: when the netdev is registered under an INTERFACE node
// (/sys/bus/usb/devices/<busid>:<cfg>.<iface>/net/<name>) rather than
// directly under the bare device, deactivateNetdevs must still find it
// and clear IFF_UP. Without this, multi-interface USB ethernet devices
// (cdc_ncm + cdc_ether on ASIX AX88179, r8152 dual-stack, …) hang onto
// the device refcount and the subsequent usbip-host bind returns EBUSY.
func TestBind_DeactivateNetdevs_CompositeInterfacePath(t *testing.T) {
	t.Parallel()

	const (
		busID   = "3-1"
		ifaceID = "3-1:2.0"
		netName = "enxAA"
	)

	mfs := bindFS(busID)
	// Composite device: netdev lives under the interface, NOT the bare
	// device. The bare-device /net/ subdir is intentionally absent so
	// the test fails if collectNetdevNames only scans there.
	mfs["sys/bus/usb/devices/"+ifaceID+"/net"] = &fstest.MapFile{Mode: fs.ModeDir}
	mfs["sys/bus/usb/devices/"+ifaceID+"/net/"+netName] = &fstest.MapFile{Mode: fs.ModeDir}
	mfs["sys/class/net/"+netName] = &fstest.MapFile{Mode: fs.ModeDir}
	mfs["sys/class/net/"+netName+"/flags"] = &fstest.MapFile{Data: []byte("0x1003\n")}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.NoError(t, err)

	flagsPath := "/sys/class/net/" + netName + "/flags"

	var clearedIFFUp bool

	for _, c := range rec.calls {
		if c.Path == flagsPath {
			clearedIFFUp = true
		}
	}

	require.True(t, clearedIFFUp,
		"deactivateNetdevs must clear IFF_UP on netdev under interface node "+
			"/sys/bus/usb/devices/%s/net/%s — composite USB-ethernet path", ifaceID, netName)
}
