// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/stretchr/testify/require"
)

// TestRemoveSubscriberAndDetachSerializesWithSubscribe pins the lock
// order behind last-unsubscribe teardown. If subscriber removal ran
// before acquiring dispMu, a concurrent Subscribe could reuse the
// dispatcher in the empty/detach gap and receive a channel that the
// teardown immediately closes.
func TestRemoveSubscriberAndDetachSerializesWithSubscribe(t *testing.T) {
	t.Parallel()

	harness := kernel.NewDetachSerializationHarness()
	harness.LockAdapter()

	started := make(chan struct{})
	detached := make(chan bool, 1)

	go func() {
		close(started)

		detached <- harness.Remove()
	}()

	<-started

	// The helper is blocked on dispMu, so it must not mutate the
	// subscriber map first.
	select {
	case <-detached:
		t.Fatal("subscriber removal bypassed dispatcher serialization")
	case <-time.After(25 * time.Millisecond):
	}

	require.Equal(t, 1, harness.SubscriberCount())

	harness.UnlockAdapter()

	require.True(t, <-detached)
	require.True(t, harness.Detached())
	require.Zero(t, harness.SubscriberCount())
}
