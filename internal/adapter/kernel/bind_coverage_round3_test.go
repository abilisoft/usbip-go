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

// TestBind_HubGuard_NonENOENTReadErrorPropagates pins refuseHubDevice's
// I/O-error branch: when bDeviceClass is unreadable for a reason
// other than ENOENT (EACCES, EIO), the guard must surface the
// failure rather than swallow it. Without this branch covered, a
// permission lock-down on /sys/bus/usb/devices/<busid>/bDeviceClass
// would silently let a hub through to the destructive unbind path.
func TestBind_HubGuard_NonENOENTReadErrorPropagates(t *testing.T) {
	t.Parallel()

	const busID = testRootBusID

	mfs := bindFS(busID)

	mfs["sys/bus/usb/devices/"+busID+"/bDeviceClass"] = &fstest.MapFile{Data: []byte(testZeroDeviceClassRaw)}

	poisoned := poisonFS{
		inner:     mfs,
		target:    "sys/bus/usb/devices/" + busID + "/bDeviceClass",
		injectErr: fs.ErrPermission,
	}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(poisoned),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrPermission,
		"hub guard must surface non-ENOENT read errors so operators see permission misconfiguration")

	require.Empty(t, rec.calls,
		"hub guard error must fire BEFORE any sysfs write")
}

// TestBind_HubGuard_NonHubClassPasses pins refuseHubDevice's happy
// path: when bDeviceClass is present and != 0x09 (HUB), the guard
// returns nil and Bind proceeds. Covers the clean-exit branch that
// the other hub tests skip — they either poison the read or set
// the value to the hub class.
func TestBind_HubGuard_NonHubClassPasses(t *testing.T) {
	t.Parallel()

	const busID = testRootBusID

	mfs := bindFS(busID)

	mfs["sys/bus/usb/devices/"+busID+"/bDeviceClass"] = &fstest.MapFile{Data: []byte(testZeroDeviceClassRaw)}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.NoError(t, err,
		"non-hub bDeviceClass must NOT block Bind")
}

// TestListExportedDevices_PropagatesListLocalErr pins
// ListExportedDevices's error-pass-through: when the underlying
// ListLocalDevices fails (e.g. kernel modules went missing
// mid-flight), the wire-side filter must surface that signal
// rather than silently returning an empty devlist that peers would
// misinterpret as "no devices to import".
func TestListExportedDevices_PropagatesListLocalErr(t *testing.T) {
	t.Parallel()

	// Empty FS — no usbip-host driver dir, ModulesAvailable fails.
	a, err := kernel.NewExporterAdapter(kernel.WithFS(fstest.MapFS{}))
	require.NoError(t, err)

	_, err = a.ListExportedDevices(context.Background())
	require.Error(t, err,
		"ListExportedDevices must propagate the ListLocalDevices error rather than masking it")
}

// TestListExportedDevices_NoUsbipStatusAttr_HiddenFromList pins
// isExportable's strict contract for a missing usbip_status file:
// when the kernel has not populated the attribute, the device is
// not yet attachable and MUST be hidden from the export list.
// Advertising it would have peers attempt attaches the kernel
// rejects; hiding it briefly during rebind is the safer trade-off.
func TestListExportedDevices_NoUsbipStatusAttr_HiddenFromList(t *testing.T) {
	t.Parallel()

	const busID = testRootBusID

	// Reuse the listExported fixture but drop the usbip_status file
	// for the lone usbip-host bound device.
	mfs := listExportedFS()
	delete(mfs, "sys/bus/usb/devices/2-1")
	delete(mfs, "sys/bus/usb/devices/3-1")
	delete(mfs, "sys/bus/usb/devices/"+busID+"/usbip_status")

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	devs, err := a.ListExportedDevices(context.Background())
	require.NoError(t, err)
	require.Empty(t, devs,
		"a usbip-host device with absent usbip_status must NOT appear in the "+
			"export list — peers would otherwise attempt attaches the kernel rejects")
}
