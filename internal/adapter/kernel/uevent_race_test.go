//go:build linux

package kernel_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
)

// TestSubscribe_ConcurrentInitOpensOneSocket proves the RANK 4 race.
// Previously EventsAdapter.dispMu was lazy-initialised inside
// initDispatcherMu: the `if a.dispMu == nil` check was itself
// unsynchronized so two goroutines racing the first Subscribe would
// each allocate a fresh *sync.Mutex and lock DIFFERENT mutexes,
// letting ensureDispatcher() call nlDial twice and leaving one
// dispatcher and its netlink socket leaked forever.
//
// Under -race the old code surfaces a data race on the dispMu pointer
// write + the underlying dialCount observation. Post-fix the mutex is
// initialised in NewEventsAdapter so the race vanishes and the dialer
// is called exactly once.
func TestSubscribe_ConcurrentInitOpensOneSocket(t *testing.T) {
	t.Parallel()

	var dialCount atomic.Int32

	dialer := func() (kernel.NetlinkSocket, error) {
		dialCount.Add(1)

		return newFakeSocket(), nil
	}

	a, err := kernel.NewEventsAdapter(kernel.WithNetlinkDialer(dialer))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const subscribers = 100

	var (
		wg      sync.WaitGroup
		cancels = make([]func(), subscribers)
		cancMu  sync.Mutex
		start   = make(chan struct{})
	)

	for i := range subscribers {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			<-start

			_, unsub, serr := a.Subscribe(ctx)
			require.NoError(t, serr)

			cancMu.Lock()
			cancels[idx] = unsub
			cancMu.Unlock()
		}(i)
	}

	close(start)
	wg.Wait()

	require.EqualValues(t, 1, dialCount.Load(),
		"concurrent first-Subscribes must share one dispatcher and one netlink socket")

	for _, unsub := range cancels {
		unsub()
	}
}
