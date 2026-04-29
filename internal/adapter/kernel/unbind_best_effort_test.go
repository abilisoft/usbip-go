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

// TestUnbind_UnbindFails_SkipsRebind pins the kernel-safety contract:
// when usbip-host/unbind fails, Unbind MUST NOT write to rebind.
// Kernels through 6.8 (Pi 5) NULL-deref in do_rebind() when the
// per-busid stub has no live udev — exactly the state when unbind
// failed (no stub_probe ran, or device was already detached).
//
// Setup: writeFunc fails on usbip-host/unbind (index 1).
// Test asserts:
//   - sockfd + unbind + match_busid del are attempted (3 writes)
//   - rebind is NOT attempted
//   - The primary error (EBUSY → ErrDeviceAlreadyBound) is surfaced
func TestUnbind_UnbindFails_SkipsRebind(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(boundFS(string(busID))),
		// errAt(1, EBUSY): write index 1 is usbip-host/unbind
		// (index 0 is the pre-disconnect sockfd write whose error
		// is intentionally swallowed).
		kernel.WithWriteFunc(rec.errAt(1, unix.EBUSY)),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"the FIRST error (usbip-host unbind EBUSY) must be surfaced as the primary return")

	require.Len(t, rec.calls, 3,
		"unbind failure must skip rebind to avoid kernel NULL deref in do_rebind")

	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/unbind", rec.calls[1].Path)
	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/match_busid", rec.calls[2].Path)

	for _, c := range rec.calls {
		require.NotEqual(t, "/sys/bus/usb/drivers/usbip-host/rebind", c.Path,
			"rebind write must NOT fire when unbind failed — Pi 5 kernel 6.8 NULL-derefs")
	}
}
