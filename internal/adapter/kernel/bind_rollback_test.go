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

// TestBind_RollsBackMatchBusidOnBindFailure pins upstream
// usbip_bind.c bind_device's compensating semantics: when the
// final usbip-host/bind write fails, the previously-added
// match_busid entry MUST be rolled back. Without rollback the busid
// table is poisoned with an entry whose driver never bound — later
// `usbip-go list -l` and operator workflows would surface a phantom
// state.
//
// Setup: writeFunc fails on the bind index (call #2). Test
// asserts:
//
//   - Bind returns the EBUSY error (primary, not a rollback error)
//   - The recorded calls include the match_busid del rollback
//     write at index 3 (after the failed bind).
func TestBind_RollsBackMatchBusidOnBindFailure(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		// errAt(2, EBUSY): the third write (usbip-host bind) fails.
		// Rollback (match_busid del) is the fourth write — see
		// assertion below.
		kernel.WithWriteFunc(rec.errAt(2, unix.EBUSY)),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"primary error (EBUSY → ErrDeviceAlreadyBound) must surface; rollback failure does not mask it")

	require.Len(t, rec.calls, 4,
		"expected calls: unbind, match_busid add, bind (failed), match_busid del rollback")

	// Rollback write at index 3.
	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/match_busid", rec.calls[3].Path,
		"rollback must target match_busid")
	require.Equal(t, "del "+string(busID), rec.calls[3].Data,
		"rollback must DELETE the busid entry that the failed bind left orphaned")
}
