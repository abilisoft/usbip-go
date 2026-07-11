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

// TestBind_SkipsUnbindWhenInterfaceHasNoDriver pins the half-state
// recovery path: an interface left without a driver (after a
// previous failed bind, or fresh hot-plug pre-probe) must NOT
// surface ErrDeviceNotBound from the bind command. There is
// nothing to unbind in step 1, so the routine should skip directly
// to step 2 (match_busid add) + step 3 (usbip-host bind).
//
// Without this carve-out, `usbip-go bind 3-1` returns "no driver
// attached to 3-1:1.0" and forces the operator to manually
// trigger /sys/bus/usb/drivers_probe to re-attach a driver they
// then immediately want unbound.
func TestBind_SkipsUnbindWhenInterfaceHasNoDriver(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	iface := string(busID) + ":1.0"

	// Bind fixture WITHOUT the driver_name file or the driver
	// symlink — interface dir exists but currentDriver yields
	// ErrDeviceNotBound.
	mfs := fstest.MapFS{
		testFSModuleUSBIPCorePath:                                       &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleUSBIPHostPath:                                       &fstest.MapFile{Mode: fs.ModeDir},
		testFSUSBIPHostDir:                                              &fstest.MapFile{Mode: fs.ModeDir},
		testFSUSBIPHostMatchBusIDPath:                                   &fstest.MapFile{Data: []byte("")},
		testFSUSBIPHostBindPath:                                         &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + string(busID):                          &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + string(busID) + "/bConfigurationValue": &fstest.MapFile{Data: []byte("1\n")},
		"sys/bus/usb/devices/" + iface:                                  &fstest.MapFile{Mode: fs.ModeDir},
		// Deliberately no driver/driver_name and no driver symlink.
	}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.NoError(t, err,
		"Bind must succeed against an interface that has no driver — there is nothing to unbind in step 1")

	// Exactly two writes when there is no old driver: match_busid + bind.
	require.Len(t, rec.calls, 2,
		"with no driver to unbind, the sequence is just match_busid add + usbip-host bind")
	require.Equal(t, testUSBIPHostMatchBusIDPath, rec.calls[0].Path)
	require.Equal(t, testUSBIPHostBindPath, rec.calls[1].Path)
	require.Equal(t, string(busID), rec.calls[1].Data,
		"usbip-host bind takes the BARE busid")
}
