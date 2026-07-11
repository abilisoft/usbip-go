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

// TestBind_DeactivateNetdev_InvalidFlagsHex pins that deactivateNetdevs
// continues gracefully when the flags file contains non-hex content.
// The kernel always emits "0x%x" for /sys/class/net/<name>/flags, but
// fuzz/corrupt sysfs reads can contain arbitrary bytes. The preflight
// must not abort Bind on a parse failure — it logs at debug and skips
// that netdev, so Bind may still EBUSY but at least tries.
func TestBind_DeactivateNetdev_InvalidFlagsHex(t *testing.T) {
	t.Parallel()

	const (
		busID   = testRootBusID
		netName = "enxAA01"
	)

	mfs := fstest.MapFS{
		testFSModuleUSBIPCorePath:                                  &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleUSBIPHostPath:                                  &fstest.MapFile{Mode: fs.ModeDir},
		testFSUSBIPHostDir:                                         &fstest.MapFile{Mode: fs.ModeDir},
		testFSUSBIPHostMatchBusIDPath:                              &fstest.MapFile{Data: []byte("")},
		testFSUSBIPHostBindPath:                                    &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + busID:                             &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/bConfigurationValue":    &fstest.MapFile{Data: []byte("1\n")},
		"sys/bus/usb/devices/" + busID + ":1.0":                    &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + ":1.0/driver/driver_name": &fstest.MapFile{Data: []byte("cdc_ether\n")},
		"sys/bus/usb/drivers/cdc_ether":                            &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/cdc_ether/unbind":                     &fstest.MapFile{Data: []byte("")},
		// Network sysfs hooks with INTENTIONALLY INVALID flags — not hex.
		// deactivateNetdevs must skip this netdev (debug-log + continue)
		// rather than aborting the whole Bind sequence.
		"sys/bus/usb/devices/" + busID + "/net":            &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/net/" + netName: &fstest.MapFile{Mode: fs.ModeDir},
		"sys/class/net/" + netName:                         &fstest.MapFile{Mode: fs.ModeDir},
		"sys/class/net/" + netName + "/flags":              &fstest.MapFile{Data: []byte("not-hex\n")},
	}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	// Bind must succeed even though the flags parse failed for the netdev.
	// The preflight is best-effort: a parse error means "skip that netdev"
	// not "abort bind".
	err = a.Bind(context.Background(), domain.BusID(busID))
	require.NoError(t, err,
		"deactivateNetdevs must continue on a ParseUint error and not abort Bind")

	// The flags write must NOT appear — we skipped the netdev.
	for _, c := range rec.calls {
		require.NotEqual(t, "/sys/class/net/"+netName+"/flags", c.Path,
			"a flags parse error must skip the netdev; no write must be emitted for it")
	}
}
