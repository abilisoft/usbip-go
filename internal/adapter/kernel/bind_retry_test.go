// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_RetriesEBUSYOnUSBIPHostBind pins the auto-retry contract:
// the kernel needs a brief window to release the device's refcount
// after the old interface driver is unbound. The first usbip-host
// bind write often returns EBUSY because the network stack (or
// other driver) is still draining queued operations on the device.
// Without retry, the operator hits "device or resource busy",
// drives a manual `ip link set <netdev> down` workaround, and
// retries by hand — every time.
//
// Bind must transparently retry the usbip-host bind step on EBUSY
// with a small backoff, and only surface the EBUSY when retries are
// exhausted.
func TestBind_RetriesEBUSYOnUSBIPHostBind(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")

	var mu sync.Mutex
	bindAttempts := 0

	writeFn := func(p, _ string) error {
		// Step 0 (unbind old driver) succeeds.
		// Step 1 (match_busid add) succeeds.
		// Step 2+ (usbip-host bind) returns EBUSY for the first
		// attempt, succeeds on the second.
		if p == "/sys/bus/usb/drivers/usbip-host/bind" {
			mu.Lock()
			bindAttempts++
			n := bindAttempts
			mu.Unlock()

			if n == 1 {
				return unix.EBUSY
			}
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(writeFn),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.NoError(t, err,
		"Bind must transparently retry the usbip-host bind step on EBUSY; the kernel needs a moment to release the device refcount after the old driver unbind")

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 2, bindAttempts,
		"first attempt EBUSY → second attempt succeeds → exactly 2 bind writes total")
}
