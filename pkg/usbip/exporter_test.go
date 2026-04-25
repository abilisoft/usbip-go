// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"context"
	"net"
	"testing"
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// exporterStubs mirrors importerStubs: a struct-return avoids blank
// identifiers so dogsled and lll stay satisfied.
type exporterStubs struct {
	inner  *internalapp.Exporter
	kernel *stubExporterKernel
	events *stubKernelEvents
	trans  *stubTransport
	codec  *stubCodec
}

// newInternalExporterForTest assembles an internal Exporter with
// stubbed adapters. A per-stub handle is exposed so each test rewires
// the behaviour it asserts.
func newInternalExporterForTest(t *testing.T) exporterStubs {
	t.Helper()

	s := exporterStubs{
		kernel: &stubExporterKernel{},
		events: &stubKernelEvents{},
		trans:  &stubTransport{},
		codec:  &stubCodec{},
	}

	s.inner = internalapp.NewExporter(
		internalapp.WithExporterKernel(s.kernel),
		internalapp.WithExporterEvents(s.events),
		internalapp.WithExporterTransport(s.trans),
		internalapp.WithExporterCodec(s.codec),
	)

	return s
}

// TestExporterWrapperNotNil proves the simplest facade wrap: given a
// non-nil internal Exporter, the public wrapper is non-nil.
func TestExporterWrapperNotNil(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	exp := usbip.NewExporterFromInternalForTest(s.inner)

	require.NotNil(t, exp)
}

// TestExporterListAvailableForwards exercises the forwarding of
// ListAvailable from facade to kernel stub.
func TestExporterListAvailableForwards(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	want := []domain.Device{{BusID: "1-1"}, {BusID: "2-1"}}

	s.kernel.listLocalDevicesFn = func(_ context.Context) ([]domain.Device, error) {
		return want, nil
	}

	exp := usbip.NewExporterFromInternalForTest(s.inner)

	t.Cleanup(shutdownCleanup(t, exp))

	got, err := exp.ListAvailable(t.Context())
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestExporterBindUnbindForwards exercises the forwarding of Bind and
// Unbind through the facade.
func TestExporterBindUnbindForwards(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	var (
		boundBus   domain.BusID
		unboundBus domain.BusID
	)

	s.kernel.bindFn = func(_ context.Context, busID domain.BusID) error {
		boundBus = busID

		return nil
	}

	s.kernel.unbindFn = func(_ context.Context, busID domain.BusID) error {
		unboundBus = busID

		return nil
	}

	exp := usbip.NewExporterFromInternalForTest(s.inner)

	t.Cleanup(shutdownCleanup(t, exp))

	require.NoError(t, exp.Bind(t.Context(), usbip.BusID("1-1")))
	require.Equal(t, usbip.BusID("1-1"), boundBus)

	require.NoError(t, exp.Unbind(t.Context(), usbip.BusID("2-1")))
	require.Equal(t, usbip.BusID("2-1"), unboundBus)
}

// TestExporterSessionsForwards exercises Sessions() returning an empty
// slice — the stubbed exporter has no live sessions so the returned
// slice is empty-but-non-nil.
func TestExporterSessionsForwards(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	exp := usbip.NewExporterFromInternalForTest(s.inner)

	t.Cleanup(shutdownCleanup(t, exp))

	got := exp.Sessions(t.Context())
	require.Empty(t, got)
}

// TestExporterShutdownForwards proves Shutdown reaches the internal
// Exporter and returns nil on an idle wrapper.
func TestExporterShutdownForwards(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	exp := usbip.NewExporterFromInternalForTest(s.inner)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)

	require.NoError(t, exp.Shutdown(ctx))
}

// TestExporterWatchSessionsForwards proves the facade WatchSessions
// returns an iter.Seq that drains the internal subscriber channel.
// The subscriber is torn down on Shutdown, so iteration terminates
// deterministically.
func TestExporterWatchSessionsForwards(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	exp := usbip.NewExporterFromInternalForTest(s.inner)

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	t.Cleanup(cancel)

	seq := exp.WatchSessions(ctx)

	// Shutdown closes subscribers so the iter terminates — drain it
	// to completion on a goroutine so the main test observes exit.
	events := make(chan int, 1)

	go func() {
		// The stubbed exporter emits no events, so the range loop
		// only terminates when Shutdown closes the subscriber.
		seen := 0

		for range seq {
			seen++
		}

		events <- seen
	}()

	require.NoError(t, exp.Shutdown(ctx))

	select {
	case seen := <-events:
		require.Zero(t, seen)
	case <-time.After(time.Second):
		t.Fatal("WatchSessions iter did not terminate after Shutdown")
	}
}

// TestExporterServeRejectsAfterShutdown proves the facade Serve
// forwards Shutdown-induced errors classified as
// usbip.ErrExporterShutdown — the public sentinel the facade
// translates from internal/app.ErrAlreadyShutdown. The stronger
// boundary assertion (no internal-sentinel leak) lives in
// TestExporterServeAfterShutdownYieldsPublicSentinel.
func TestExporterServeRejectsAfterShutdown(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	exp := usbip.NewExporterFromInternalForTest(s.inner)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	t.Cleanup(cancel)

	require.NoError(t, exp.Shutdown(ctx))

	// Serve after Shutdown must return the public exporter-shutdown
	// sentinel (translated from the internal identity).
	err := exp.Serve(ctx, stubListener{})
	require.ErrorIs(t, err, usbip.ErrExporterShutdown)
}

// shutdownCleanup returns a t.Cleanup func that Shutdowns exp with a
// detached bounded context. t.Context() is already cancelled by the
// time cleanups run, which would propagate as "exporter shutdown: ctx
// canceled"; a fresh context keeps the cleanup assertion meaningful.
func shutdownCleanup(t *testing.T, exp *usbip.Exporter) func() {
	t.Helper()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		require.NoError(t, exp.Shutdown(ctx))
	}
}

// stubListener is a minimal net.Listener that never accepts a conn;
// its sole purpose is to satisfy Serve's signature so the post-shutdown
// branch fires deterministically.
type stubListener struct{}

// Accept blocks until Close is called. Tests always short-circuit via
// ctx cancel / Shutdown before reaching a real accept.
func (stubListener) Accept() (net.Conn, error) { return nil, errNotImplemented }

// Close is a no-op so tests do not have to manage a real socket.
func (stubListener) Close() error { return nil }

// Addr returns a synthetic TCP4 address.
func (stubListener) Addr() net.Addr { return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0} }
