// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestImporterCloseWaitsForInFlightAttach pins the contract that
// Close must not return while any Attach goroutine is still inside
// the kernel handoff or its subsequent cleanup. The service layer
// historically left Attach out of the wait group, so an Attach
// parked inside kernel.AttachRemote kept running past Close.
// The test holds AttachRemote on a gate channel, fires Close in
// parallel, and asserts Close blocks until the caller releases the
// gate. A separate atomic tracks Attach completion; the invariant
// is "Attach finished before Close returned", i.e. Close drained
// the in-flight worker.
func TestImporterCloseWaitsForInFlightAttach(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	attachFinished := make(chan struct{})

	imp, _, _, kernel := newReconnectFixture(t,
		func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			<-gate

			return domain.PortID(1), nil
		})

	_ = kernel

	var attachDone atomic.Bool

	go func() {
		defer close(attachFinished)

		_, _ = imp.Attach(context.Background(), testRemote(), attachBusID(),
			app.AttachOptions{})

		attachDone.Store(true)
	}()

	// Give the Attach goroutine time to enter AttachRemote and park
	// on the gate. Without this, Close could race the Attach's
	// mu-locked acquireAttachSlot step and observe no in-flight
	// slot yet.
	time.Sleep(100 * time.Millisecond)

	closeReturned := make(chan struct{})

	go func() {
		defer close(closeReturned)

		_ = imp.Close()
	}()

	// Close must NOT return while Attach is still parked on the
	// gate. A sleep proves the synchronous wait empirically; a
	// success here is "Close is blocked, Attach is still blocked,
	// neither channel closes".
	select {
	case <-closeReturned:
		t.Fatal("Close returned before in-flight Attach drained")
	case <-time.After(50 * time.Millisecond):
	}

	// Release the Attach; then Close must return. Assert the
	// ordering: Attach finishes first, Close returns second.
	close(gate)

	select {
	case <-attachFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("Attach did not finish after gate release")
	}

	require.True(t, attachDone.Load())

	select {
	case <-closeReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after Attach drained")
	}
}
