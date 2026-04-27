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

	// Persistently fail every usbip-host/bind write so the retry
	// budget is exhausted and the rollback runs. Other writes
	// (driver unbind, match_busid add, match_busid del) succeed.
	persistentEBUSYOnBind := func(p, data string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: data})
		rec.mu.Unlock()

		if p == "/sys/bus/usb/drivers/usbip-host/bind" {
			return unix.EBUSY
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(persistentEBUSYOnBind),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"primary error (EBUSY → ErrDeviceAlreadyBound) must surface; rollback failure does not mask it")

	// Last recorded write must be the match_busid del rollback.
	last := rec.calls[len(rec.calls)-1]
	require.Equal(t, "/sys/bus/usb/drivers/usbip-host/match_busid", last.Path,
		"rollback must target match_busid (last recorded write)")
	require.Equal(t, "del "+string(busID), last.Data,
		"rollback must DELETE the busid entry that the failed bind left orphaned")
}
