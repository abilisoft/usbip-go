// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestImporterAttachConcurrentSameEndpoint pins the Attach dedup
// contract: two goroutines calling Attach with the same (remote,
// busid) must serialise — exactly ONE completes the kernel handoff and
// the other returns ErrAttachInProgress. Without the guard both
// Attach calls would race the dial + handshake + AttachRemote sequence
// and import the same device onto TWO local ports, corrupting the
// handle map.
func TestImporterAttachConcurrentSameEndpoint(t *testing.T) {
	t.Parallel()

	// releaseAttach gates kernel.AttachRemote so we can be certain
	// both goroutines have entered Attach before the first handoff
	// completes — the exact ordering the dedup guards against.
	releaseAttach := make(chan struct{})

	var (
		nextID       atomic.Uint32
		concurrentIn atomic.Int32
	)

	attachFn := func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		concurrentIn.Add(1)

		<-releaseAttach

		return domain.PortID(nextID.Add(1)), nil
	}

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc:     attachFn,
		DetachPortFunc:       func(_ context.Context, _ domain.PortID) error { return nil },
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	var (
		wg        sync.WaitGroup
		results   = make([]error, 2)
		resultsMu sync.Mutex
		ports     [2]domain.Port
	)

	start := make(chan struct{})

	for i := range 2 {
		wg.Add(1)

		go func(idx int) {
			defer wg.Done()

			<-start

			port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})

			resultsMu.Lock()
			results[idx] = err
			ports[idx] = port
			resultsMu.Unlock()
		}(i)
	}

	close(start)

	// Wait until at least one goroutine has entered AttachRemote and
	// the other is waiting on the dedup — the dedup lock is the only
	// reason the second goroutine cannot reach AttachRemote.
	require.Eventually(t, func() bool {
		return concurrentIn.Load() >= 1
	}, 2*time.Second, 5*time.Millisecond)

	// Hold briefly so the losing goroutine has a chance to return
	// ErrAttachInProgress.
	time.Sleep(50 * time.Millisecond)

	close(releaseAttach)
	wg.Wait()

	var oks, busies int

	for _, err := range results {
		if err == nil {
			oks++

			continue
		}

		require.ErrorIs(t, err, app.ErrAttachInProgress)

		busies++
	}

	require.Equal(t, 1, oks, "exactly one concurrent Attach must succeed")
	require.Equal(t, 1, busies, "the loser must observe ErrAttachInProgress")

	require.EqualValues(t, 1, concurrentIn.Load(),
		"AttachRemote must be called EXACTLY once despite concurrent Attach callers")
}
