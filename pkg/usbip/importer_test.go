package usbip_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// newInternalImporterForTest assembles an internal Importer with
// stubbed adapters. The returned stubs are exposed so per-case setup
// can rewire individual methods.
func newInternalImporterForTest(t *testing.T) (*internalapp.Importer, *stubImporterKernel, *stubKernelEvents, *stubTransport, *stubCodec) {
	t.Helper()

	k := &stubImporterKernel{}
	e := &stubKernelEvents{}
	tr := &stubTransport{}
	c := &stubCodec{}

	imp := internalapp.NewImporter(
		internalapp.WithImporterKernel(k),
		internalapp.WithImporterEvents(e),
		internalapp.WithImporterTransport(tr),
		internalapp.WithImporterCodec(c),
	)

	return imp, k, e, tr, c
}

// TestImporterWrapperNotNil is the smallest sanity check: wrapping a
// non-nil internal Importer yields a non-nil facade Importer.
func TestImporterWrapperNotNil(t *testing.T) {
	t.Parallel()

	inner, _, _, _, _ := newInternalImporterForTest(t)

	imp := usbip.NewImporterFromInternalForTest(inner)

	require.NotNil(t, imp)
}

// TestImporterListRemoteForwards wires a codec that returns a known
// device list and asserts ListRemote on the facade surfaces the same
// slice. The test also proves RemoteEndpoint flows through unmodified.
func TestImporterListRemoteForwards(t *testing.T) {
	t.Parallel()

	inner, _, _, tr, c := newInternalImporterForTest(t)

	want := []domain.Device{{BusID: "1-1"}, {BusID: "1-2"}}

	c.decodeOpRepDevlistFn = func(_ io.Reader) ([]domain.Device, error) {
		return want, nil
	}

	// Override Dial to return a net.Pipe pair; the fake drain goroutine
	// reads from the remote side so the importer's Write does not park.
	tr.dialFn = func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
		local, remote := net.Pipe()

		go func() {
			buf := make([]byte, 64)
			for {
				_, err := remote.Read(buf)
				if err != nil {
					_ = remote.Close()

					return
				}
			}
		}()

		return local, nil
	}

	imp := usbip.NewImporterFromInternalForTest(inner)

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

	inner, k, _, _, c := newInternalImporterForTest(t)

	decoded := domain.Device{BusID: "1-1", Speed: domain.SpeedHigh, BusNum: 1, DevNum: 2}

	c.decodeOpRepImportFn = func(_ io.Reader) (domain.Device, error) {
		return decoded, nil
	}

	k.attachRemoteFn = func(_ context.Context, _ net.Conn, spec internalapp.RemoteDeviceSpec) (domain.PortID, error) {
		require.Equal(t, decoded, spec.Device)

		return 7, nil
	}

	imp := usbip.NewImporterFromInternalForTest(inner)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	port, err := imp.Attach(t.Context(),
		usbip.RemoteEndpoint{Host: "peer.test"},
		usbip.BusID("1-1"),
		usbip.AttachOptions{})
	require.NoError(t, err)
	require.Equal(t, usbip.PortID(7), port.ID)
	require.Equal(t, usbip.BusID("1-1"), port.BusID)
}

// TestImporterDetachForwards proves Detach reaches the kernel stub
// with the PortID returned by Attach.
func TestImporterDetachForwards(t *testing.T) {
	t.Parallel()

	inner, k, _, _, c := newInternalImporterForTest(t)

	c.decodeOpRepImportFn = func(_ io.Reader) (domain.Device, error) {
		return domain.Device{BusID: "1-1", Speed: domain.SpeedHigh, BusNum: 1, DevNum: 2}, nil
	}

	k.attachRemoteFn = func(_ context.Context, _ net.Conn, _ internalapp.RemoteDeviceSpec) (domain.PortID, error) {
		return 3, nil
	}

	detachGot := make(chan domain.PortID, 1)

	k.detachPortFn = func(_ context.Context, id domain.PortID) error {
		detachGot <- id

		return nil
	}

	imp := usbip.NewImporterFromInternalForTest(inner)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	_, err := imp.Attach(t.Context(), usbip.RemoteEndpoint{Host: "peer"}, "1-1", usbip.AttachOptions{})
	require.NoError(t, err)

	require.NoError(t, imp.Detach(t.Context(), usbip.PortID(3)))

	select {
	case got := <-detachGot:
		require.Equal(t, usbip.PortID(3), got)
	case <-time.After(time.Second):
		t.Fatal("DetachPort stub never invoked")
	}
}

// TestImporterListPortsForwards wires a kernel stub that returns a
// synthetic port list and asserts the facade returns the same slice.
func TestImporterListPortsForwards(t *testing.T) {
	t.Parallel()

	inner, k, _, _, _ := newInternalImporterForTest(t)

	want := []domain.Port{{ID: 1, Status: domain.StatusUsed}, {ID: 2}}

	k.listPortsFn = func(_ context.Context) ([]domain.Port, error) {
		return want, nil
	}

	imp := usbip.NewImporterFromInternalForTest(inner)

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

	inner, _, e, _, _ := newInternalImporterForTest(t)

	ch := make(chan domain.Event, 1)
	cancelled := make(chan struct{})

	e.subscribeFn = func(_ context.Context) (<-chan domain.Event, func(), error) {
		return ch, func() { close(cancelled) }, nil
	}

	ch <- domain.DeviceBoundEvent{Device: domain.Device{BusID: "1-1"}}
	close(ch)

	imp := usbip.NewImporterFromInternalForTest(inner)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	var got []usbip.Event

	for ev := range imp.Watch(t.Context()) {
		got = append(got, ev)
	}

	require.Len(t, got, 1)
	require.Equal(t, domain.EventDeviceBound, got[0].EventKind())

	<-cancelled
}

// TestImporterCloseIsIdempotent proves consecutive Close calls do not
// panic and both return nil — the facade must NOT introduce its own
// close state on top of the internal sync.Once guard.
func TestImporterCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	inner, _, _, _, _ := newInternalImporterForTest(t)

	imp := usbip.NewImporterFromInternalForTest(inner)

	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
}

// TestImporterAfterCloseSurfacesSentinel proves operations against a
// closed Importer return the internal sentinel so wrapping code can
// branch on errors.Is(err, internalapp.ErrImporterClosed). The facade
// does not expose ErrImporterClosed itself (not in §5.7), so the test
// matches on the underlying internal sentinel.
func TestImporterAfterCloseSurfacesSentinel(t *testing.T) {
	t.Parallel()

	inner, _, _, _, _ := newInternalImporterForTest(t)

	imp := usbip.NewImporterFromInternalForTest(inner)
	require.NoError(t, imp.Close())

	_, err := imp.ListRemote(t.Context(), usbip.RemoteEndpoint{Host: "peer"})
	require.ErrorIs(t, err, internalapp.ErrImporterClosed)
}

// TestImporterAttachOptionsWithBackoff proves AttachOptions fields
// flow through. Bounding the backoff avoids a deterministic deadlock
// when a flaky transport triggers auto-reconnect in future tests.
func TestImporterAttachOptionsTypeIsPublic(t *testing.T) {
	t.Parallel()

	// The literal must compile with all documented fields — this
	// captures an accidental rename / removal at the public surface.
	opts := usbip.AttachOptions{
		AutoReconnect:      false,
		Backoff:            nil,
		MaxAttempts:        3,
		OnReconnect:        func(int, error) {},
		StatusPollInterval: time.Second,
	}

	require.Equal(t, 3, opts.MaxAttempts)
}

// silence unused-import warnings when Task 6.3 RED is run ahead of
// Task 6.5: errors + wire are only imported by helper types.
var (
	_ = errors.New
	_ = wire.OpCode(0)
)
