// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// blockingListener holds every Accept call forever so the firstAccept
// hook semantics can be observed without any real client. Close sends
// net.ErrClosed to unblock outstanding Accepts; tests cancel via
// t.Cleanup so goroutine leaks are caught by the package's goleak
// harness (when configured) or by the race detector's scheduler.
type blockingListener struct {
	accepts atomic.Int32
	done    chan struct{}
}

// newBlockingListener returns a listener whose Accept blocks on an
// unbuffered done channel. Close closes done so outstanding Accepts
// return net.ErrClosed.
func newBlockingListener() *blockingListener {
	return &blockingListener{done: make(chan struct{})}
}

// Accept blocks until Close fires. Increment-on-entry lets tests
// observe that the accept loop has entered Accept — the signal
// /readyz requires before reporting 200.
func (l *blockingListener) Accept() (net.Conn, error) {
	l.accepts.Add(1)

	<-l.done

	return nil, net.ErrClosed
}

func (l *blockingListener) Close() error {
	select {
	case <-l.done:
		return nil
	default:
		close(l.done)

		return nil
	}
}

func (*blockingListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

// TestWrapListenerFirstAcceptFiresOnAcceptEntry pins the accept-ready
// hook contract: the hook must fire as soon as the accept loop enters
// its first Accept call, not only after that call returns a
// successful net.Conn. An idle daemon whose first Accept blocks
// forever MUST still report /readyz=200 — the readiness semantic is
// "I will accept if something connects", not "I have accepted ≥1".
func TestWrapListenerFirstAcceptFiresOnAcceptEntry(t *testing.T) {
	t.Parallel()

	lis := newBlockingListener()

	var hookFired atomic.Int32

	wrapped := wrapListenerFirstAcceptWithHook(lis, func() {
		hookFired.Add(1)
	})

	// Spawn a goroutine that parks inside Accept; the real daemon's
	// accept loop does the same. No client ever connects.
	go func() {
		_, _ = wrapped.Accept()
	}()

	// The hook MUST fire within a tight budget — the only signal the
	// test waits on is "Accept was entered". A hook that fires only
	// on first successful Accept return would leave an idle daemon
	// stuck at /readyz=503 forever.
	require.Eventually(t, func() bool {
		return hookFired.Load() >= 1
	}, 2*time.Second, 5*time.Millisecond,
		"accepting hook must fire on Accept entry so /readyz can flip 200 before any client connects")

	require.NoError(t, lis.Close())
}

// TestWrapListenerFirstAcceptHookFiresOnce guards against the hook
// firing on every Accept call — the readiness transition is one-way
// for the life of Serve.
func TestWrapListenerFirstAcceptHookFiresOnce(t *testing.T) {
	t.Parallel()

	var accepts atomic.Int32

	lis := &cyclicBlockingListener{}

	var fires atomic.Int32

	wrapped := wrapListenerFirstAcceptWithHook(lis, func() {
		fires.Add(1)
	})

	// Drive multiple Accept calls through the wrapper; each increments
	// accepts on entry, and the inner listener returns net.ErrClosed
	// so the loop exits without blocking indefinitely.
	for range 5 {
		_, _ = wrapped.Accept()

		accepts.Add(1)
	}

	require.EqualValues(t, 1, fires.Load(),
		"hook must fire exactly once across repeated Accept calls")
}

// cyclicBlockingListener returns net.ErrClosed on every Accept so
// TestWrapListenerFirstAcceptHookFiresOnce exercises the one-shot
// hook semantics without blocking.
type cyclicBlockingListener struct{}

func (*cyclicBlockingListener) Accept() (net.Conn, error) { return nil, errCyclicListenerClosed }
func (*cyclicBlockingListener) Close() error              { return nil }
func (*cyclicBlockingListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0}
}

var errCyclicListenerClosed = errors.New("cyclic listener closed")
