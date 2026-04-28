// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_CtxCancelsRetryBackoff pins the context-cancellation path
// inside bindUSBIPHostWithRetry. The retry loop sleeps between attempts
// via a clock.After select; a cancelled context must interrupt that
// sleep and surface context.Canceled rather than waiting for the full
// backoff to expire.
//
// Setup: inject a FakeClock (so clock.After never fires on its own),
// make the first bind write return EBUSY, and pre-cancel the context.
// When the loop reaches attempt 1, the select races ctx.Done() against
// the unfired clock channel; ctx.Done() wins immediately.
func TestBind_CtxCancelsRetryBackoff(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")

	clock := testutil.NewFakeClockAt(time.Unix(0, 0))

	// Write func: let all setup writes succeed; first bind attempt → EBUSY.
	writeFn := func(p, _ string) error {
		if p == usbipHostBindPath {
			return unix.EBUSY
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(writeFn),
		kernel.WithClock(clock),
	)
	require.NoError(t, err)

	// Pre-cancel: ctx.Done() is already closed when the retry select runs.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = a.Bind(ctx, busID)
	require.Error(t, err,
		"Bind must return an error when the retry backoff is interrupted by context cancellation")
	require.ErrorIs(t, err, context.Canceled,
		"the error must wrap context.Canceled so callers can distinguish cancellation from EBUSY exhaustion")
}

// TestBind_VHCIGuard_ErrInvalidMeansNotASymlink pins that
// refuseVHCIBindLoop treats fs.ErrInvalid as "entry is not a symlink"
// and returns nil (guard inapplicable). Under fstest.MapFS, directories
// are plain entries — ReadLink surfaces ErrInvalid rather than
// ErrNotExist because the entry exists but has no symlink target.
// The guard must not abort Bind for this case; the inapplicable branch
// is safe to continue.
//
// Bind will still fail because the minimal FS has no bConfigurationValue
// or driver files. The important assertion is that the error is NOT from
// the vhci guard itself.
func TestBind_VHCIGuard_ErrInvalidMeansNotASymlink(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("3-1")

	mfs := fstest.MapFS{
		"sys/module/usbip_core":                &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host":                &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + string(busID): &fstest.MapFile{Mode: fs.ModeDir},
	}

	wrapped := &errorReadLinkFS{
		inner:     mfs,
		linkError: fs.ErrInvalid,
	}

	a, err := kernel.NewExporterAdapter(kernel.WithFS(wrapped))
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)

	// The vhci guard must have passed (ErrInvalid → inapplicable).
	// Bind fails later (no bConfigurationValue), NOT with ErrPermission.
	require.Error(t, err,
		"Bind must fail (no device setup), but not at the vhci guard stage")
	require.False(t, errors.Is(err, fs.ErrPermission),
		"ErrInvalid must not be treated as a fail-closed permission fault")
	require.False(t, errors.Is(err, domain.ErrDeviceAlreadyBound),
		"ErrInvalid must not trigger the vhci-loop refusal")
}
