// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"maps"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// deviceSysfs builds a MapFS that mimics /sys/bus/usb/devices/<busid>/
// with the per-device attribute set ListLocalDevices reads, plus a
// primary interface descriptor under <busid>:1.0.
func deviceSysfs(busID string, attrs map[string]string) fstest.MapFS {
	m := fstest.MapFS{}

	for name, value := range attrs {
		m["sys/bus/usb/devices/"+busID+"/"+name] = &fstest.MapFile{Data: []byte(value)}
	}

	iface := busID + ":1.0"

	m["sys/bus/usb/devices/"+iface] = &fstest.MapFile{Mode: fs.ModeDir}
	m["sys/bus/usb/devices/"+iface+"/bInterfaceClass"] = &fstest.MapFile{Data: []byte("09\n")}
	m["sys/bus/usb/devices/"+iface+"/bInterfaceSubClass"] = &fstest.MapFile{Data: []byte("00\n")}
	m["sys/bus/usb/devices/"+iface+"/bInterfaceProtocol"] = &fstest.MapFile{Data: []byte("00\n")}
	m["sys/bus/usb/devices/"+iface+"/bAlternateSetting"] = &fstest.MapFile{Data: []byte("0\n")}

	return m
}

func makeDeviceAttrs() map[string]string {
	return map[string]string{
		"idVendor":            "0x0951\n",
		"idProduct":           "0x1666\n",
		"bcdDevice":           "1100\n",
		"busnum":              "1\n",
		"devnum":              "7\n",
		"speed":               "3\n",
		"bDeviceClass":        "00\n",
		"bDeviceSubClass":     "00\n",
		"bDeviceProtocol":     "00\n",
		"bConfigurationValue": "1\n",
		"bNumConfigurations":  "1\n",
		"bNumInterfaces":      "1\n",
	}
}

// moduleDirs returns the two /sys/module/<name> entries needed for the
// exporter-side ModulesAvailable preflight to pass.
func moduleDirs() fstest.MapFS {
	return fstest.MapFS{
		"sys/module/usbip_core": &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host": &fstest.MapFile{Mode: fs.ModeDir},
	}
}

// mergeFS combines multiple MapFS into a single MapFS (later keys win).
func mergeFS(base ...fstest.MapFS) fstest.MapFS {
	out := fstest.MapFS{}

	for _, m := range base {
		maps.Copy(out, m)
	}

	return out
}

func TestListLocalDevices_FiltersBusIDLikeEntries(t *testing.T) {
	t.Parallel()

	dev := deviceSysfs("1-1", makeDeviceAttrs())

	// Add non-device entries the walker must ignore.
	mfs := mergeFS(dev, moduleDirs(), fstest.MapFS{
		"sys/bus/usb/devices/usb1":    &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/1-0:1.0": &fstest.MapFile{Mode: fs.ModeDir},
	})

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, domain.BusID("1-1"), got[0].BusID)
	require.Equal(t, uint16(0x0951), got[0].VendorID)
	require.Equal(t, uint16(0x1666), got[0].ProductID)
	require.Equal(t, uint16(0x1100), got[0].BcdDevice)
	require.Equal(t, uint16(1), got[0].BusNum)
	require.Equal(t, uint16(7), got[0].DevNum)
	require.Equal(t, domain.SpeedHigh, got[0].Speed)
	require.Equal(t, uint8(1), got[0].ConfigValue)
	require.Equal(t, uint8(1), got[0].NumConfigs)
	require.Equal(t, uint8(1), got[0].NumInterfaces)
	require.Len(t, got[0].Interfaces, 1)
	require.Equal(t, domain.USBClass(0x09), got[0].Interfaces[0].Class)
}

func TestListLocalDevices_MultipleDevices(t *testing.T) {
	t.Parallel()

	devA := deviceSysfs("1-1", makeDeviceAttrs())
	devB := deviceSysfs("1-1.2", makeDeviceAttrs())

	mfs := mergeFS(devA, devB, moduleDirs())

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)

	busIDs := map[domain.BusID]bool{}
	for _, d := range got {
		busIDs[d.BusID] = true
	}

	require.True(t, busIDs["1-1"])
	require.True(t, busIDs["1-1.2"])
}

// TestListLocalDevices_ModuleMissingReturnsBoth asserts v1 contract §3.4's
// contract: when /sys/module/usbip_core is absent, both the nil slice
// and ErrKernelModuleMissing are returned.
func TestListLocalDevices_ModuleMissingReturnsBoth(t *testing.T) {
	t.Parallel()

	dev := deviceSysfs("1-1", makeDeviceAttrs())
	// No module dirs.
	mfs := dev

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.Empty(t, got)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

// TestListLocalDevices_MissingBusRoot surfaces the degenerate case
// where /sys/bus/usb/devices doesn't exist at all. Because the parent
// directory path is a DEVICE path (kindDevice), its ENOENT surfaces as
// ErrDeviceNotFound — callers interpreting this see "no devices".
func TestListLocalDevices_MissingBusRoot(t *testing.T) {
	t.Parallel()

	mfs := moduleDirs()

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.Empty(t, got)
	require.ErrorIs(t, err, domain.ErrDeviceNotFound)
}
