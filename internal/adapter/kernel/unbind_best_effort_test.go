// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestUnbind_AttemptsRebindEvenAfterEarlierFailure pins the
// best-effort contract: when usbip-host/unbind fails, Unbind STILL
// attempts the match_busid del and the rebind trigger so the
// device's match_busid table entry is cleared and the original
// kernel driver can take the device back. Without this, an
// intermediate failure would leave the system in a half-state
// where the operator sees the device but neither usbip-host nor
// the original driver claim it.
//
// Setup: writeFunc fails on the FIRST write (usbip-host unbind).
// Test asserts:
//   - All three writes are attempted (length == 3)
//   - The primary error (EBUSY) is surfaced, not a downstream error
func TestUnbind_AttemptsRebindEvenAfterEarlierFailure(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		// errAt(1, EBUSY): write index 1 is the usbip-host/unbind
		// (index 0 is the pre-disconnect sockfd write whose error
		// is intentionally swallowed). Mock the FIRST classified
		// failure on the actual unbind path.
		kernel.WithWriteFunc(rec.errAt(1, unix.EBUSY)),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"the FIRST error (usbip-host unbind EBUSY) must be surfaced as the primary return")

	require.Len(t, rec.calls, 4,
		"all four sysfs writes must be attempted even after the unbind fails — best-effort cleanup")

	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/unbind", rec.calls[1].Path)
	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/match_busid", rec.calls[2].Path)
	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/rebind", rec.calls[3].Path)
}
