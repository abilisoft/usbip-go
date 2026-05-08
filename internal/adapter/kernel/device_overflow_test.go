//go:build linux

package kernel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
)

// TestListLocalDevices_BusnumOverflowFailsClosed pins the overflow
// fail-closed contract. Sysfs busnum / devnum fields are u16 on wire;
// a value past 0xFFFF is either a kernel bug or a maliciously-injected
// sysfs entry. ReadDevice fails the whole device read so
// ListLocalDevices skips the entry and the caller sees only well-
// formed devices, rather than silently masking the high bits
// (uint16(v & 0xFFFF)) and reporting a nonsense BusNum.
func TestListLocalDevices_BusnumOverflowFailsClosed(t *testing.T) {
	t.Parallel()

	attrs := makeDeviceAttrs()

	attrs["busnum"] = "65536\n" // 0x10000 — one past u16 max.

	dev := deviceSysfs("1-1", attrs)
	mfs := mergeFS(dev, moduleDirs())

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)
	require.Empty(t, got,
		"sysfs busnum past u16 max must not surface as a truncated device entry")
}

// TestListLocalDevices_DevnumOverflowFailsClosed covers the matching
// devnum field.
func TestListLocalDevices_DevnumOverflowFailsClosed(t *testing.T) {
	t.Parallel()

	attrs := makeDeviceAttrs()

	attrs["devnum"] = "70000\n"

	dev := deviceSysfs("1-1", attrs)
	mfs := mergeFS(dev, moduleDirs())

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)
	require.Empty(t, got,
		"sysfs devnum past u16 max must not surface as a truncated device entry")
}

// TestListLocalDevices_ConfigValueOverflowFailsClosed covers the u8
// sysfs fields (readByteAttr must not silently mask).
func TestListLocalDevices_ConfigValueOverflowFailsClosed(t *testing.T) {
	t.Parallel()

	attrs := makeDeviceAttrs()

	attrs["bConfigurationValue"] = "256\n" // 0x100 — one past u8 max.

	dev := deviceSysfs("1-1", attrs)
	mfs := mergeFS(dev, moduleDirs())

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)
	require.Empty(t, got,
		"sysfs byte-width field past u8 max must not silently truncate")
}

// TestListLocalDevices_InterfaceOverflowFailsClosed pins the
// interface-overflow fail-closed contract. readInterface validates
// each byte-width interface descriptor field (bInterfaceClass,
// bInterfaceSubClass, bInterfaceProtocol, bAlternateSetting) and
// returns errSysfsValueOutOfRange on overflow. Overflow errors are
// fatal for the device read; the whole entry is skipped rather than
// surfaced with a silently-partial Interfaces slice (which would hide
// malformed sysfs data). Only genuine "file does not exist" errors
// (ENOENT on optional attrs) are still tolerated.
func TestListLocalDevices_InterfaceOverflowFailsClosed(t *testing.T) {
	t.Parallel()

	dev := deviceSysfs("1-1", makeDeviceAttrs())

	// Override the interface class to a value exceeding u8 max.
	// ReadHex16 parses this as 0x100 and narrowByteErr surfaces
	// errSysfsValueOutOfRange.
	dev["sys/bus/usb/devices/1-1:1.0/bInterfaceClass"].Data = []byte("0100\n")

	mfs := mergeFS(dev, moduleDirs())

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := a.ListLocalDevices(context.Background())
	require.NoError(t, err)
	require.Empty(t, got,
		"sysfs interface-class overflow must fail the whole device "+
			"read — emitting the device with a silently-truncated "+
			"Interfaces slice would hide malformed sysfs data")
}
