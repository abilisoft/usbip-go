// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"path"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestUnbind_DisconnectsSockfdBeforeUnbindWrite pins the
// pre-disconnect contract: Unbind first writes "-1" to the
// per-device usbip_sockfd attribute to drop any active importer
// session, THEN writes to usbip-host/unbind. Without this
// pre-step the usbip-host unbind blocks indefinitely while the
// kernel waits for in-flight URBs to drain through a still-open
// importer socket. Operators who tried to unbind a busy device
// would see `usbip-go unbind` hang and be forced to kill -9.
//
// Order asserted by the recorded write list:
//  1. /sys/bus/usb/devices/<busid>/usbip_sockfd  ← "-1"
//  2. /sys/bus/usb/drivers/usbip-host/unbind     ← <busid>
//  3. /sys/bus/usb/drivers/usbip-host/match_busid ← "del <busid>"
//  4. /sys/bus/usb/drivers/usbip-host/rebind     ← <busid>
func TestUnbind_DisconnectsSockfdBeforeUnbindWrite(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(boundFS(string(busID))),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), busID)
	require.NoError(t, err)

	require.Len(t, rec.calls, 4,
		"expected sockfd disconnect + the existing 3 unbind writes")

	require.Equal(t,
		path.Join(kernel.SysfsUSBDevices, string(busID), kernel.SysfsUsbipSockfd),
		rec.calls[0].Path,
		"first write must target the per-device usbip_sockfd attribute")
	require.Equal(t, kernel.UsbipSockfdDisconnect, rec.calls[0].Data,
		"first write must be the disconnect payload (-1) so the kernel drops any active session before the driver unbind")

	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/unbind", rec.calls[1].Path)
	require.Equal(t, testUSBIPHostMatchBusIDPath, rec.calls[2].Path)
	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/rebind", rec.calls[3].Path)
}
