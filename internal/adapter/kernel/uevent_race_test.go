// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
)

// TestSubscribe_ConcurrentInitOpensOneSocket pins the
// single-dispatcher contract for concurrent first Subscribers.
// Lazy-initialising EventsAdapter.dispMu inside the first Subscribe
// would make the `if a.dispMu == nil` check unsynchronised so two
// goroutines racing the first Subscribe would each allocate a fresh
// *sync.Mutex, lock DIFFERENT mutexes, let ensureDispatcher() call
// nlDial twice, and leak one dispatcher and its netlink socket
// forever.
//
// The mutex is initialised in NewEventsAdapter so the race vanishes
// and the dialer is called exactly once; the race detector would
// otherwise surface a data race on the dispMu pointer write and the
// underlying dialCount observation.
func TestSubscribe_ConcurrentInitOpensOneSocket(t *testing.T) {
	t.Parallel()

	var dialCount atomic.Int32

	dialer := func() (kernel.NetlinkSocket, error) {
		dialCount.Add(1)

		return newFakeSocket(), nil
	}

	a, err := kernel.NewEventsAdapter(
		kernel.WithFS(singleControllerTopoFS()),
		kernel.WithNetlinkDialer(dialer),
	)
	require.NoError(t, err)

	ctx, cancel := t.Context(), func() {}
	defer cancel()

	const subscribers = 100

	var (
		wg      sync.WaitGroup
		cancels = make([]func(), subscribers)
		cancMu  sync.Mutex
		errs    = make([]error, subscribers)
		start   = make(chan struct{})
	)

	for i := range subscribers {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			<-start

			_, unsub, serr := a.Subscribe(ctx)

			cancMu.Lock()
			cancels[idx] = unsub
			errs[idx] = serr
			cancMu.Unlock()
		}(i)
	}

	close(start)
	wg.Wait()

	for _, serr := range errs {
		require.NoError(t, serr)
	}

	require.EqualValues(t, 1, dialCount.Load(),
		"concurrent first-Subscribes must share one dispatcher and one netlink socket")

	for _, unsub := range cancels {
		unsub()
	}
}
