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

// TestBind_FailureTriggersDriversProbe pins the bind-failure recovery
// contract. After Bind detaches the bare-device driver and the
// usbip-host bind step subsequently fails, the device is left with no
// driver attached. Without an explicit recovery write, the device
// stays orphaned until the kernel's auto-probe fires (or a manual
// `udevadm trigger`). We do better: write the busid to
// /sys/bus/usb/drivers_probe so the kernel re-evaluates the driver
// match-table and re-attaches the original driver.
//
// The recovery is best-effort. If drivers_probe itself errors, we log
// and surface only the original bind error (operators care WHY bind
// failed, not WHY recovery failed).
//
// Setup: writeFunc fails on the usbip-host/bind step (after
// match_busid add succeeded). Test asserts:
//   - the original ErrDeviceAlreadyBound (EBUSY) is returned
//   - match_busid del rollback fired
//   - drivers_probe write fired with the bare busid as data
func TestBind_FailureTriggersDriversProbe(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	rec := &writeRecord{}

	writeFn := func(p, d string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: d})
		rec.mu.Unlock()

		// Fail the usbip-host bind step (4th write in the new flow:
		// bare-device unbind, match_busid add, usbip-host bind).
		if p == usbipHostBindPath {
			return unix.EBUSY
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
		"primary error (EBUSY → ErrDeviceAlreadyBound) must be returned even when recovery succeeds")

	var sawProbe, sawRollback bool

	for _, c := range rec.calls {
		if c.Path == driversProbePath && c.Data == busID {
			sawProbe = true
		}

		if c.Path == usbipHostMatchBusIDPath && c.Data == "del "+busID {
			sawRollback = true
		}
	}

	require.True(t, sawRollback,
		"match_busid del rollback must fire when usbip-host/bind fails")
	require.True(t, sawProbe,
		"drivers_probe must be written with bare busid to trigger native-driver re-attach after bind failure")
}
