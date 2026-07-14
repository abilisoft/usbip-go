// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestExporterSession_IgnoresImporterPortDetachBusIDCollision proves that a
// VHCI detach for an imported remote device cannot terminate an unrelated
// exporter session merely because both devices use the same BusID string.
// KernelEvents is role-shared; only local DeviceUnbound, authoritative
// usbip_status inactivity, or Shutdown can end the exported Session.
func TestExporterSession_IgnoresImporterPortDetachBusIDCollision(t *testing.T) {
	t.Parallel()

	const sessionBusID = domain.BusID("4-1")

	kernel := &ExporterKernelMock{
		// serveImport now looks the requested device up in the
		// exported set BEFORE sending OP_REP_IMPORT and handing the
		// fd to the kernel — return the busid so the lookup succeeds.
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: sessionBusID}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, id domain.BusID) error {
			require.Equal(t, sessionBusID, id)

			return nil
		},
	}

	events := make(chan domain.Event)

	kernelEvents := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return events, func() {}, nil
		},
	}

	codec := newSessionImportCodec(sessionBusID)

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 9), Port: 9400})

	exp := newExporterForTest(
		t,
		app.WithExporterKernel(kernel),
		app.WithExporterEvents(kernelEvents),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond,
		"Sessions() must report the session after ExportOnConn returns — the kernel still owns the fd")

	// The unbuffered send completes only after the exporter handler receives
	// the importer-role event. Matching BusID is intentional: it reproduces
	// the cross-role identity collision without scheduler sleeps.
	select {
	case events <- domain.PortDetachedEvent{Port: domain.Port{BusID: sessionBusID}}:
	case <-time.After(2 * time.Second):
		t.Fatal("session handler did not start waiting for kernel events")
	}

	require.Len(t, exp.Sessions(context.Background()), 1,
		"importer PortDetachedEvent must not terminate the colliding exporter Session")

	require.Len(t, kernel.ExportOnConnCalls(), 1,
		"ExportOnConn should have been invoked exactly once")

	// A role-correct local unbind remains a terminal exporter event.
	select {
	case events <- domain.DeviceUnboundEvent{
		At:     time.Now(),
		Device: domain.Device{BusID: sessionBusID},
	}:
	case <-time.After(2 * time.Second):
		t.Fatal("session handler stopped receiving kernel events")
	}

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 0
	}, 2*time.Second, 10*time.Millisecond,
		"Sessions() must empty after the matching exporter DeviceUnboundEvent")

	cancel()

	require.NoError(t, <-serveDone)
}

// TestExporterSession_ShutdownEndsKernelOwnedSession asserts that
// Shutdown unwinds a handler parked in waitForSessionEnd. The handler
// must exit without the kernel emitting a detach event — Shutdown
// cancels handle.done alongside its graceful Disconnect call so the
// waiter terminates with DisconnectReasonShutdown even if the mock
// kernel's Disconnect is a silent no-op.
func TestExporterSession_ShutdownEndsKernelOwnedSession(t *testing.T) {
	t.Parallel()

	const sessionBusID = domain.BusID("4-2")

	kernel := &ExporterKernelMock{
		// serveImport now looks the requested device up in the
		// exported set BEFORE sending OP_REP_IMPORT and handing the
		// fd to the kernel — return the busid so the lookup succeeds.
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: sessionBusID}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			return nil
		},
		// Shutdown invokes Disconnect per active session. Silent no-op
		// here; handle.cancel() drives the actual handler exit for
		// this scenario.
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	// Events channel stays open but never delivers — Shutdown must
	// still terminate the parked handler via handle.cancel() (the
	// handle.done branch of waitForSessionEnd).
	events := make(chan domain.Event)

	kernelEvents := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return events, func() {}, nil
		},
	}

	codec := newSessionImportCodec(sessionBusID)

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 10), Port: 9500})

	exp := newExporterForTest(
		t,
		app.WithExporterKernel(kernel),
		app.WithExporterEvents(kernelEvents),
		app.WithExporterCodec(codec),
	)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	require.NoError(t, exp.Shutdown(shutdownCtx),
		"Shutdown must drain the parked handler without hanging")

	require.Empty(t, exp.Sessions(context.Background()),
		"Sessions() must be empty after Shutdown drains parked handlers")

	cancel()

	<-serveDone
}
