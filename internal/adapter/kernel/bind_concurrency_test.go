// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_ConcurrentSameBusid_Serialized pins the per-busid lock
// contract: two concurrent Bind() calls against the same busid MUST
// be serialised by an internal mutex so the kernel match_busid table
// is never mutated by overlapping callers. Without this, a loser's
// rollback (match_busid del) can race ahead of the winner's bind
// step and erase the just-added entry.
//
// The test interposes a write function that detects overlap: it
// records the count of in-flight callers; if >1 ever, fail. Each call
// holds for 5ms inside writeClassified to widen any race window.
func TestBind_ConcurrentSameBusid_Serialized(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	var (
		inFlight atomic.Int32
		maxSeen  atomic.Int32
	)

	writeFn := func(_, _ string) error {
		now := inFlight.Add(1)

		for {
			seen := maxSeen.Load()
			if now <= seen || maxSeen.CompareAndSwap(seen, now) {
				break
			}
		}

		time.Sleep(5 * time.Millisecond)
		inFlight.Add(-1)

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(busID)),
		kernel.WithWriteFunc(writeFn),
	)
	require.NoError(t, err)

	const concurrency = 8

	var wg sync.WaitGroup

	wg.Add(concurrency)

	for range concurrency {
		go func() {
			defer wg.Done()

			_ = a.Bind(context.Background(), domain.BusID(busID))
		}()
	}

	wg.Wait()

	require.Equal(t, int32(1), maxSeen.Load(),
		"per-busid lock must serialise Bind() — saw %d concurrent sysfs writers", maxSeen.Load())
}
