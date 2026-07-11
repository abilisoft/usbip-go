// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_AlreadyBoundToUSBIPHost_ShortCircuits pins the
// already-bound contract: when the bare device's driver symlink is
// already "usbip-host", Bind returns ErrDeviceAlreadyBound BEFORE
// writing to match_busid. Without this short-circuit the kernel
// surfaces EBUSY at the bind step, which is the same error class but
// reaches the user one round-trip later — and after partial sysfs
// writes that may need rolling back.
func TestBind_AlreadyBoundToUSBIPHost_ShortCircuits(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	mfs := bindFS(string(busID))
	// Override the bare-device driver to usbip-host (production state
	// after a successful prior bind).
	mfs["sys/bus/usb/devices/"+string(busID)+"/driver/driver_name"].Data = []byte("usbip-host\n")
	mfs["sys/bus/usb/devices/"+string(busID)+"/driver"].Data = []byte("usbip-host\n")

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"Bind must short-circuit with ErrDeviceAlreadyBound when device's driver is already usbip-host")

	for _, c := range rec.calls {
		require.NotEqual(t, testUSBIPHostMatchBusIDPath, c.Path,
			"already-bound short-circuit must fire BEFORE match_busid is written")
		require.NotEqual(t, testUSBIPHostBindPath, c.Path,
			"already-bound short-circuit must fire BEFORE the bind write")
	}
}

// TestBind_NoDeviceDriverSkipsDeviceUnbind pins the absent-driver
// branch of unbindCurrentDeviceDriver: when the bare device has no
// driver symlink (rare, but happens during transient hot-plug states),
// Bind skips the device-level unbind and continues to match_busid.
func TestBind_NoDeviceDriverSkipsDeviceUnbind(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	mfs := bindFS(string(busID))
	delete(mfs, "sys/bus/usb/devices/"+string(busID)+"/driver/driver_name")
	delete(mfs, "sys/bus/usb/devices/"+string(busID)+"/driver")

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.NoError(t, err)

	for _, c := range rec.calls {
		require.NotEqual(t, "/sys/bus/usb/drivers/usb/unbind", c.Path,
			"device-level unbind must NOT fire when bare device has no driver attached")
	}
}
