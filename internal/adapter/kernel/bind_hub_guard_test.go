// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_RefuseHubDevice_NoSysfsWrites pins the hub guard contract:
// when the bare device's bDeviceClass is 0x09 (HUB) Bind MUST refuse
// without performing any destructive sysfs write. Without this guard,
// detaching the generic "usb" driver from a hub disconnects every
// downstream device hanging off it before usbip-host's stub_probe
// rejects the bind in drivers/usb/usbip/stub_dev.c. Upstream
// usbip_bind.c::unbind_other() reads bDeviceClass and rejects hubs at
// lines 82-91; the kernel checks again at stub_dev.c:347-351. We refuse
// at the earliest possible point — before any unbind write.
func TestBind_RefuseHubDevice_NoSysfsWrites(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	mfs := bindFS(busID)
	// USB hub class — the device the user is trying to export is a hub.
	mfs["sys/bus/usb/devices/"+busID+"/bDeviceClass"] = &fstest.MapFile{Data: []byte("09\n")}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.Error(t, err, "Bind must refuse hub devices")
	require.ErrorIs(t, err, domain.ErrUnsupportedDevice,
		"hub refusal must surface as ErrUnsupportedDevice so operators see WHY (not a kernel-level errno)")

	require.Empty(t, rec.calls,
		"hub guard MUST fire BEFORE any sysfs write — detaching the generic usb driver "+
			"from a hub disconnects all downstream devices")
}
