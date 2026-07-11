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

// TestUnbind_PreDisconnectSocketfdWriteFails_Continues pins the
// pre-disconnect best-effort contract: when the usbip_sockfd write
// (index 0) fails, Unbind logs at debug and continues. The failure
// is non-fatal — a missing active session means there is nothing to
// disconnect. All four sysfs writes must still be attempted and the
// function must return nil when the subsequent writes succeed.
func TestUnbind_PreDisconnectSocketfdWriteFails_Continues(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(boundFS(string(busID))),
		// errAt(0, EIO): the sockfd pre-disconnect write (index 0) fails;
		// all subsequent writes (unbind, match_busid, rebind) succeed.
		kernel.WithWriteFunc(rec.errAt(0, unix.EIO)),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), busID)
	require.NoError(t, err,
		"sockfd pre-disconnect failure is non-fatal; Unbind must return nil when subsequent writes succeed")

	require.Len(t, rec.calls, 4,
		"all four sysfs writes must be attempted even when the pre-disconnect sockfd write fails")
}

// TestUnbind_UnbindAndMatchBothFail_WarnLog pins the switch branch
// where both the usbip-host/unbind write and the subsequent match_busid
// del write fail. The unbind error is the primary return; the match
// error triggers a Warn log (not a panic and not a return masking the
// primary). The rebind write is SKIPPED because unbind failed (would
// NULL-deref kernel <=6.8 in do_rebind).
//
// Write order (by index):
//
//	0: usbip_sockfd  ← pre-disconnect, non-fatal
//	1: unbind        ← FAILS → unbindErr (primary)
//	2: match_busid   ← ALSO FAILS → logged at Warn (not returned)
//	(rebind skipped — kernel-safety contract)
func TestUnbind_UnbindAndMatchBothFail_WarnLog(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	rec := &writeRecord{}

	failAt1and2 := func(p, d string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: d})
		rec.mu.Unlock()

		n := len(rec.calls) - 1
		if n == 1 {
			return unix.ENODEV
		}

		if n == 2 {
			return unix.EIO
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(boundFS(string(busID))),
		kernel.WithWriteFunc(kernel.WriteFunc(failAt1and2)),
	)
	require.NoError(t, err)

	unbindErr := a.Unbind(context.Background(), busID)

	require.Error(t, unbindErr,
		"unbind failure must be returned as the primary error")
	require.NotErrorIs(t, unbindErr, domain.ErrDeviceAlreadyBound,
		"unbind ENODEV must not be classified as ErrDeviceAlreadyBound")

	require.Len(t, rec.calls, 3,
		"unbind failure must skip rebind (kernel NULL-deref guard); match_busid del still attempted")

	for _, c := range rec.calls {
		require.NotEqual(t, "/sys/bus/usb/drivers/usbip-host/rebind", c.Path,
			"rebind write must NOT fire when unbind failed")
	}
}

// TestUnbind_MatchAndRebindBothFail_WarnLog pins the post-success-unbind
// path where match_busid del fails AND the rebind trigger also fails.
// match_busid is the primary return (unbind succeeded so that error is
// nil); rebind is logged at Warn.
//
// Write order (by index):
//
//	0: usbip_sockfd  ← pre-disconnect, non-fatal
//	1: unbind        ← succeeds
//	2: match_busid   ← FAILS → matchErr (primary)
//	3: rebind        ← ALSO FAILS → logged at Warn (not returned)
func TestUnbind_MatchAndRebindBothFail_WarnLog(t *testing.T) {
	t.Parallel()

	busID := domain.BusID(testRootBusID)
	rec := &writeRecord{}

	failAt2and3 := func(p, d string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: d})
		rec.mu.Unlock()

		n := len(rec.calls) - 1
		if n == 2 {
			return unix.EIO
		}

		if n == 3 {
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
		"match_busid del failure must be returned as the primary error when unbind succeeded")

	require.Len(t, rec.calls, 4,
		"unbind success must NOT skip rebind; all four writes attempted in this scenario")
}
