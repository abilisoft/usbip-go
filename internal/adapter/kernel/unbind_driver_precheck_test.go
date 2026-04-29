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

// TestUnbind_NotBoundToUSBIPHost_ReturnsErrNoWrites pins the
// unbind-precheck contract: if the bare device's driver is anything
// other than "usbip-host" Unbind MUST return ErrDeviceNotBound and
// MUST NOT mutate sysfs.
//
// Upstream usbip_unbind.c::unbind_device() performs the same check at
// lines 54-58 — if the driver isn't usbip-host, it bails before any
// write. Without this guard, our Unbind erroneously writes
// usbip-host/unbind and match_busid del for a busid the driver never
// owned, polluting state on devices we don't manage.
func TestUnbind_NotBoundToUSBIPHost_ReturnsErrNoWrites(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	mfs := bindFS(busID)
	// Bare device's driver is the generic "usb" driver — NOT usbip-host.
	// (bindFS already configures this; explicit for clarity.)
	mfs["sys/bus/usb/devices/"+busID+"/driver/driver_name"].Data = []byte("usb\n")
	mfs["sys/bus/usb/devices/"+busID+"/driver"].Data = []byte("usb\n")

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), domain.BusID(busID))
	require.ErrorIs(t, err, domain.ErrDeviceNotBound,
		"Unbind on a non-usbip-host device must return ErrDeviceNotBound")

	require.Empty(t, rec.calls,
		"Unbind precheck must refuse BEFORE any sysfs write — match_busid del on a non-managed device pollutes kernel state")
}
