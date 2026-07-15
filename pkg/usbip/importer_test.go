// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// importerStubs bundles the mock adapters so tests can reach for the
// handle they care about without tolerating blank identifiers. Using a
// struct also keeps the helper signature short enough to pass lll.
type importerStubs struct {
	inner  *internalapp.Importer
	kernel *stubImporterKernel
	events *stubKernelEvents
	trans  *stubTransport
	codec  *stubCodec
}

// facadeLineageBackoff exposes mutable state so a facade-level factory test can
// prove two logical Attachments do not share the strategy returned to either.
type facadeLineageBackoff struct {
	resetCount int
}

func (*facadeLineageBackoff) Next(_ int) time.Duration { return 0 }

func (b *facadeLineageBackoff) Reset() { b.resetCount++ }

// newInternalImporterForTest assembles an internal Importer with
// stubbed adapters. The returned bundle exposes each stub so per-case
// setup can rewire a single behaviour.
func newInternalImporterForTest(t *testing.T) importerStubs {
	t.Helper()

	s := importerStubs{
		kernel: &stubImporterKernel{},
		events: &stubKernelEvents{},
		trans:  &stubTransport{},
		codec:  &stubCodec{},
	}

	s.inner = internalapp.NewImporter(
		internalapp.WithImporterKernel(s.kernel),
		internalapp.WithImporterEvents(s.events),
		internalapp.WithImporterTransport(s.trans),
		internalapp.WithImporterCodec(s.codec),
	)

	return s
}

// TestImporterWrapperNotNil is the smallest sanity check: wrapping a
// non-nil internal Importer yields a non-nil facade Importer.
func TestImporterWrapperNotNil(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	require.NotNil(t, imp)
}

// TestImporterListRemoteForwards wires a codec that returns a known
// device list and asserts ListRemote on the facade surfaces the same
// slice. The test also proves RemoteEndpoint flows through unmodified.
func TestImporterListRemoteForwards(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	want := []domain.Device{{BusID: testRootBusID}, {BusID: "1-2"}}

	s.codec.decodeOpRepDevlistFn = func(_ io.Reader) ([]domain.Device, error) {
		return want, nil
	}

	s.trans.dialFn = pipeDialer()

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	got, err := imp.ListRemote(t.Context(), usbip.RemoteEndpoint{Host: "peer.test", Port: 3240})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestImporterAttachForwards drives Attach through the facade and
// proves the decoded Device/PortID reach the caller unchanged.
func TestImporterAttachForwards(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	decoded := domain.Device{BusID: testRootBusID, Speed: domain.SpeedHigh, BusNum: 1, DevNum: 2}

	s.codec.decodeOpRepImportFn = func(_ io.Reader) (domain.Device, error) {
		return decoded, nil
	}

	s.kernel.attachRemoteFn = func(
		_ context.Context, _ net.Conn, spec internalapp.RemoteDeviceSpec,
	) (domain.PortID, error) {
		require.Equal(t, decoded, spec.Device)

		return 7, nil
	}

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	port, err := imp.Attach(t.Context(),
		usbip.RemoteEndpoint{Host: "peer.test"},
		usbip.BusID(testRootBusID),
		usbip.AttachOptions{})
	require.NoError(t, err)
	require.Equal(t, usbip.PortID(7), port.ID)
	require.Equal(t, usbip.BusID(testRootBusID), port.BusID)
}

func TestImporterBackoffFactoryCreatesOneIsolatedStrategyPerTopLevelAttach(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)
	devices := []domain.Device{
		{BusID: testRootBusID, Speed: domain.SpeedHigh, BusNum: 1, DevNum: 2},
		{BusID: "1-2", Speed: domain.SpeedHigh, BusNum: 1, DevNum: 3},
	}
	portIDs := []domain.PortID{7, 8}
	decodeCall := 0
	attachCall := 0

	s.codec.decodeOpRepImportFn = func(_ io.Reader) (domain.Device, error) {
		require.Less(t, decodeCall, len(devices))

		device := devices[decodeCall]
		decodeCall++

		return device, nil
	}

	s.kernel.attachRemoteFn = func(
		_ context.Context, _ net.Conn, _ internalapp.RemoteDeviceSpec,
	) (domain.PortID, error) {
		require.Less(t, attachCall, len(portIDs))

		portID := portIDs[attachCall]
		attachCall++

		return portID, nil
	}

	strategies := make([]*facadeLineageBackoff, 0, len(devices))
	imp := usbip.NewImporterFromInternalForTest(
		s.inner,
		usbip.WithImporterBackoffFactory(func() usbip.BackoffStrategy {
			strategy := &facadeLineageBackoff{}

			strategies = append(strategies, strategy)

			return strategy
		}),
	)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	for idx, device := range devices {
		port, err := imp.Attach(
			t.Context(),
			usbip.RemoteEndpoint{Host: testPeerHost},
			device.BusID,
			usbip.AttachOptions{AutoReconnect: true, StatusPollInterval: -1},
		)
		require.NoError(t, err)
		require.Equal(t, portIDs[idx], port.ID)
	}

	require.Len(t, strategies, len(devices),
		"the configured factory must run once for each top-level logical Attachment")
	require.NotSame(t, strategies[0], strategies[1])

	strategies[0].Reset()
	require.Equal(t, 1, strategies[0].resetCount)
	require.Zero(t, strategies[1].resetCount,
		"mutating one logical Attachment's strategy must not affect another")
}

// TestImporterDetachForwards proves Detach reaches the kernel stub
// with the PortID returned by Attach.
func TestImporterDetachForwards(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	s.codec.decodeOpRepImportFn = func(_ io.Reader) (domain.Device, error) {
		return domain.Device{BusID: testRootBusID, Speed: domain.SpeedHigh, BusNum: 1, DevNum: 2}, nil
	}

	s.kernel.attachRemoteFn = func(
		_ context.Context, _ net.Conn, _ internalapp.RemoteDeviceSpec,
	) (domain.PortID, error) {
		return 3, nil
	}

	detachGot := make(chan domain.PortID, 1)

	s.kernel.detachPortFn = func(_ context.Context, id domain.PortID) error {
		detachGot <- id

		return nil
	}

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	_, err := imp.Attach(t.Context(), usbip.RemoteEndpoint{Host: testPeerHost}, testRootBusID, usbip.AttachOptions{})
	require.NoError(t, err)

	require.NoError(t, imp.Detach(t.Context(), usbip.PortID(3)))

	select {
	case got := <-detachGot:
		require.Equal(t, usbip.PortID(3), got)
	case <-time.After(time.Second):
		t.Fatal("DetachPort stub never invoked")
	}
}

// TestImporterDetachFreshInstanceForwards proves the public facade does not
// require a process-local Attach before detaching a kernel-owned Port.
func TestImporterDetachFreshInstanceForwards(t *testing.T) {
	t.Parallel()

	const portID = usbip.PortID(6)

	s := newInternalImporterForTest(t)
	detachGot := make(chan domain.PortID, 1)

	s.kernel.detachPortFn = func(_ context.Context, id domain.PortID) error {
		detachGot <- id

		return nil
	}

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	require.NoError(t, imp.Detach(t.Context(), portID))

	select {
	case got := <-detachGot:
		require.Equal(t, portID, got)
	case <-time.After(time.Second):
		t.Fatal("fresh Importer did not forward Detach to the kernel")
	}
}

// TestImporterListPortsForwards wires a kernel stub that returns a
// synthetic port list and asserts the facade returns the same slice.
func TestImporterListPortsForwards(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	want := []domain.Port{{ID: 1, Status: domain.StatusUsed}, {ID: 2}}

	s.kernel.listPortsFn = func(_ context.Context) ([]domain.Port, error) {
		return want, nil
	}

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	got, err := imp.ListPorts(t.Context())
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestImporterWatchYieldsEvents proves the facade Watch returns an
// iter.Seq that yields events from the KernelEvents source. The test
// closes the upstream channel to terminate the iterator deterministically.
func TestImporterWatchYieldsEvents(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	ch := make(chan domain.Event, 1)
	cancelled := make(chan struct{})

	s.events.subscribeFn = func(_ context.Context) (<-chan domain.Event, func(), error) {
		return ch, func() { close(cancelled) }, nil
	}

	ch <- domain.DeviceBoundEvent{Device: domain.Device{BusID: testRootBusID}}

	close(ch)

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	got := make([]usbip.Event, 0, 1)

	for ev := range imp.Watch(t.Context()) {
		got = append(got, ev)
	}

	require.Len(t, got, 1)
	require.Equal(t, domain.EventDeviceBound, got[0].EventKind())

	<-cancelled
}

func TestImporterWatchWithErrorsTranslatesUnexpectedClosure(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	ch := make(chan domain.Event)
	close(ch)

	s.events.subscribeFn = func(_ context.Context) (<-chan domain.Event, func(), error) {
		return ch, func() {}, nil
	}

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	count := 0

	for event, watchErr := range imp.WatchWithErrors(t.Context()) {
		require.Nil(t, event)
		require.ErrorIs(t, watchErr, usbip.ErrEventStreamClosed)
		require.NotErrorIs(t, watchErr, internalapp.ErrEventStreamClosed)

		count++
	}

	require.Equal(t, 1, count)
}

func TestImporterWatchWithErrorsPreservesSubscribeCause(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	s.events.subscribeFn = func(_ context.Context) (<-chan domain.Event, func(), error) {
		return nil, nil, errNotImplemented
	}

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	count := 0

	for event, watchErr := range imp.WatchWithErrors(t.Context()) {
		require.Nil(t, event)
		require.ErrorIs(t, watchErr, errNotImplemented)

		count++
	}

	require.Equal(t, 1, count)
}

func TestImporterWatchWithErrorsEarlyBreakCancelsSubscription(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	ch := make(chan domain.Event, 1)
	ch <- domain.PortAttachedEvent{Port: domain.Port{ID: 7}}

	cancelled := make(chan struct{})

	s.events.subscribeFn = func(_ context.Context) (<-chan domain.Event, func(), error) {
		return ch, func() { close(cancelled) }, nil
	}

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	for range imp.WatchWithErrors(t.Context()) {
		break
	}

	<-cancelled
}

// TestImporterCloseIsIdempotent proves consecutive Close calls do not
// panic and both return nil — the facade must NOT introduce its own
// close state on top of the internal sync.Once guard.
func TestImporterCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	imp := usbip.NewImporterFromInternalForTest(s.inner)

	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
}

// TestImporterAfterCloseSurfacesSentinel proves operations against a
// closed Importer return the public usbip.ErrImporterClosed sentinel
// so wrapping code can classify the error via errors.Is against the
// public identity. The stronger boundary assertion (no internal-
// sentinel leak) lives in TestImporterAfterCloseYieldsPublicSentinel.
func TestImporterAfterCloseSurfacesSentinel(t *testing.T) {
	t.Parallel()

	s := newInternalImporterForTest(t)

	imp := usbip.NewImporterFromInternalForTest(s.inner)
	require.NoError(t, imp.Close())

	_, err := imp.ListRemote(t.Context(), usbip.RemoteEndpoint{Host: testPeerHost})
	require.ErrorIs(t, err, usbip.ErrImporterClosed)
}

// TestImporterAttachOptionsTypeIsPublic pins the AttachOptions shape
// by constructing the struct with every field set. An accidental
// rename or removal from the public surface surfaces here at compile
// time.
func TestImporterAttachOptionsTypeIsPublic(t *testing.T) {
	t.Parallel()

	opts := usbip.AttachOptions{
		AutoReconnect:      false,
		Backoff:            nil,
		MaxAttempts:        3,
		OnReconnect:        func(int, error) {},
		StatusPollInterval: time.Second,
	}

	require.Equal(t, 3, opts.MaxAttempts)
}
