// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_DeviceUnbindWriteFails_PropagatesError pins the
// unbindCurrentDeviceDriver write-error branch: when the kernel
// rejects the write to /sys/bus/usb/drivers/<driver>/unbind (e.g.
// EPERM on a sandboxed daemon, EIO on a transient sysfs glitch),
// Bind MUST surface the error rather than continuing to
// match_busid — proceeding would leave the device's original
// driver attached and the bind step would EBUSY anyway.
func TestBind_DeviceUnbindWriteFails_PropagatesError(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	rec := &writeRecord{}

	writeFn := func(p, d string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: d})
		rec.mu.Unlock()

		if p == "/sys/bus/usb/drivers/usb/unbind" {
			return unix.EPERM
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(busID)),
		kernel.WithWriteFunc(writeFn),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrPermission,
		"EPERM on the bare-device unbind write must surface as ErrPermission")

	for _, c := range rec.calls {
		require.NotEqual(t, usbipHostMatchBusIDPath, c.Path,
			"match_busid must NOT be written when the bare-device unbind failed")
	}
}

// TestBind_RollbackDriversProbeFails_LogsButReturnsPrimary pins the
// drivers_probe failure branch: when the bind step fails AND the
// rollback drivers_probe write also fails, the primary EBUSY is
// returned and the probe failure is logged at debug. Without
// reaching this branch coverage stays incomplete on the recovery
// path.
func TestBind_RollbackDriversProbeFails_LogsButReturnsPrimary(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	rec := &writeRecord{}

	writeFn := func(p, d string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: d})
		rec.mu.Unlock()

		switch p {
		case usbipHostBindPath:
			return unix.EBUSY
		case driversProbePath:
			return unix.EIO
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(busID)),
		kernel.WithWriteFunc(writeFn),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"primary bind failure must surface even when drivers_probe rollback fails")

	var sawProbe bool

	for _, c := range rec.calls {
		if c.Path == driversProbePath {
			sawProbe = true
		}
	}

	require.True(t, sawProbe,
		"drivers_probe must still be attempted even though it returns an error")
}

// TestUnbind_DriverReadPermissionError_SurfacesError pins the
// preflightUnbind branch where currentDriver returns a non-ENOENT
// error: Unbind must propagate the I/O signal rather than
// swallowing it. Without this branch covered, a permission failure
// on /sys/bus/usb/devices/<busid>/driver would silently become
// ErrDeviceNotBound and the operator never learns sysfs is locked
// down.
func TestUnbind_DriverReadPermissionError_SurfacesError(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	mfs := boundFS(busID)
	poisoned := poisonFS{
		inner:     mfs,
		target:    "sys/bus/usb/devices/" + busID + "/driver/driver_name",
		injectErr: fs.ErrPermission,
	}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(poisoned),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), domain.BusID(busID))
	require.ErrorIs(t, err, domain.ErrPermission,
		"Unbind must surface real I/O errors from the driver precheck, not collapse them to ErrDeviceNotBound")

	require.Empty(t, rec.calls,
		"Unbind must NOT mutate sysfs when the driver precheck cannot be evaluated")
}

// TestUnbind_NoDriverAttached_ReturnsErrNotBound pins the
// "no driver attached" branch of preflightUnbind: currentDriver
// returns ErrDeviceNotBound when both driver_name and the symlink
// are absent, and Unbind wraps it with a clarifying message before
// returning.
func TestUnbind_NoDriverAttached_ReturnsErrNotBound(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	mfs := boundFS(busID)
	delete(mfs, "sys/bus/usb/devices/"+busID+"/driver/driver_name")
	delete(mfs, "sys/bus/usb/devices/"+busID+"/driver")

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), domain.BusID(busID))
	require.ErrorIs(t, err, domain.ErrDeviceNotBound,
		"Unbind on a device with no driver attached must return ErrDeviceNotBound")

	require.Empty(t, rec.calls,
		"Unbind must NOT mutate sysfs when there's no driver to unbind from")
}
