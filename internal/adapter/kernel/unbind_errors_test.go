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

// TestUnbind_MatchBusIDErrorIsPrimaryReturn pins the switch case where
// unbind succeeds but match_busid del fails. The match error must be the
// primary return so the operator can see the root cause, and the rebind
// trigger must still be attempted (best-effort cleanup). The rebind error
// (if any) is only logged, not returned.
//
// Write index order inside Unbind:
//
//	0: /sys/bus/usb/devices/<busid>/usbip_sockfd  ← pre-disconnect (non-fatal)
//	1: /sys/bus/usb/drivers/usbip-host/unbind      ← succeeds
//	2: /sys/bus/usb/drivers/usbip-host/match_busid ← FAILS → matchErr
//	3: /sys/bus/usb/drivers/usbip-host/rebind      ← attempted, succeeds
func TestUnbind_MatchBusIDErrorIsPrimaryReturn(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(boundFS(string(busID))),
		kernel.WithWriteFunc(rec.errAt(2, unix.EIO)),
	)
	require.NoError(t, err)

	unbindErr := a.Unbind(context.Background(), busID)
	require.Error(t, unbindErr,
		"match_busid del failure must surface as the Unbind return error")

	require.Len(t, rec.calls, 4,
		"all four sysfs writes must be attempted: sockfd+unbind+match+rebind")

	require.Equal(t,
		"/sys/bus/usb/drivers/usbip-host/match_busid",
		rec.calls[2].Path,
		"failed write must be the match_busid path")
}

// TestUnbind_MatchBusIDAndRebindErrorLogsSecondary pins the inner branch
// where both match_busid and rebind fail after a successful unbind. The
// match error must still be the primary return; the rebind error must
// only be logged (not replace the primary return).
//
//	0: usbip_sockfd  ← pre-disconnect (non-fatal)
//	1: unbind        ← succeeds
//	2: match_busid   ← FAILS (match is primary)
//	3: rebind        ← ALSO FAILS (secondary, logged only)
func TestUnbind_MatchBusIDAndRebindErrorLogsSecondary(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	// Fail writes at index 2 (match_busid) and index 3 (rebind).
	failAt2and3 := func(p, d string) error {
		rec.mu.Lock()
		defer rec.mu.Unlock()

		rec.calls = append(rec.calls, writeCall{Path: p, Data: d})

		n := len(rec.calls) - 1

		if n == 2 || n == 3 {
			return unix.EIO
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(boundFS(string(busID))),
		kernel.WithWriteFunc(kernel.WriteFunc(failAt2and3)),
	)
	require.NoError(t, err)

	unbindErr := a.Unbind(context.Background(), busID)
	require.Error(t, unbindErr,
		"primary error (match_busid) must be returned even when rebind also fails")

	require.Len(t, rec.calls, 4,
		"all four writes must be attempted even when both secondary writes fail")
}

// TestUnbind_RebindErrorIsPrimaryReturn pins the case where unbind and
// match_busid both succeed but rebind fails. The rebind error must be
// returned as the primary so the operator knows the device's original
// driver was not rebound.
//
//	0: usbip_sockfd  ← pre-disconnect (non-fatal)
//	1: unbind        ← succeeds
//	2: match_busid   ← succeeds
//	3: rebind        ← FAILS → rebindErr
func TestUnbind_RebindErrorIsPrimaryReturn(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(boundFS(string(busID))),
		kernel.WithWriteFunc(rec.errAt(3, unix.EIO)),
	)
	require.NoError(t, err)

	unbindErr := a.Unbind(context.Background(), busID)
	require.Error(t, unbindErr,
		"rebind failure must surface as the Unbind return error")

	require.Len(t, rec.calls, 4,
		"all four writes must be attempted before rebind error is returned")

	require.Equal(t,
		"/sys/bus/usb/drivers/usbip-host/rebind",
		rec.calls[3].Path,
		"failed write must be the rebind path")
}
