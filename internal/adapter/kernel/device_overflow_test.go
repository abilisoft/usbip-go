//go:build linux

package kernel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
)

// TestListLocalDevices_BusnumOverflowFailsClosed proves the RANK 8
// fix. Sysfs busnum / devnum fields are u16 on wire; a value past
// 0xFFFF is either a kernel bug or a maliciously-injected sysfs entry.
// Pre-fix the reader silently masked the high bits (uint16(v & 0xFFFF))
// and reported a nonsense BusNum; post-fix ReadDevice fails the whole
// device read so ListLocalDevices skips the entry and the caller sees
// only well-formed devices.
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
// devnum field per RANK 8.
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
// sysfs fields per RANK 8 (readByteAttr used to silently mask).
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
