// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"context"
	"iter"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/abilisoft/usbip-go/test/integration"
	"github.com/stretchr/testify/require"
)

// eventCollectDeadline bounds how long each event test is willing to
// wait for a matching uevent to arrive via the netlink subscription.
// Kernel uevent emission is typically sub-millisecond; five seconds
// covers slow-VM runners without inviting hangs on a broken setup.
const (
	eventCollectDeadline = 5 * time.Second
	// eventScenarioDeadline covers VUDC enumeration, detach convergence,
	// and both event-observation windows on a cold kernel runner.
	eventScenarioDeadline = 45 * time.Second
	eventRetryInterval    = 50 * time.Millisecond
	eventGadgetName       = "usbip_go_integration_events"
	eventUDCClassRoot     = "/sys/class/udc"
	eventUeventAttribute  = "uevent"
	eventUeventChange     = "change\n"
	eventSysfsWriteMode   = 0o200
)

// TestEventsDeviceBoundUnbound exercises the production usbip-host event
// path against a dummy_hcd-backed USB device. Configfs UDC membership does
// not itself mean that a device became exportable; binding and unbinding the
// enumerated USB topology bus ID through Exporter is the real lifecycle that
// DeviceBoundEvent and DeviceUnboundEvent describe.
func TestEventsDeviceBoundUnbound(t *testing.T) {
	busID := domain.BusID(integration.SetupDummyHCDGadget(t, eventGadgetName))

	imp, err := usbip.NewImporter()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), eventCollectDeadline)
	defer cancel()

	exp, err := usbip.NewExporter()
	require.NoError(t, err)

	t.Cleanup(func() {
		sctx, scancel := context.WithTimeout(context.Background(), eventCollectDeadline)
		defer scancel()

		_ = exp.Unbind(sctx, busID)
		_ = exp.Shutdown(sctx)
	})

	type eventResult struct {
		event usbip.Event
		ok    bool
	}

	result := make(chan eventResult, 1)

	go func() {
		event, ok := awaitDeviceEvent(ctx, imp.Watch(ctx), busID)
		result <- eventResult{event: event, ok: ok}
	}()

	var got usbip.Event
	var operationErr error

	require.Eventually(t, func() bool {
		select {
		case observed := <-result:
			got = observed.event

			return observed.ok
		default:
		}

		operationErr = exp.Bind(ctx, busID)
		if operationErr != nil {
			return false
		}

		operationErr = exp.Unbind(ctx, busID)
		if operationErr != nil {
			return false
		}

		select {
		case observed := <-result:
			got = observed.event

			return observed.ok
		default:
			return false
		}
	}, eventCollectDeadline, eventRetryInterval,
		"must observe a DeviceBound or DeviceUnbound event for %s (last operation error: %v)",
		busID, operationErr)
	require.NoError(t, operationErr)

	// Either ordering is valid: bind-then-unbind (subscription caught
	// the original UDC bind) OR unbind only (subscription arrived
	// after the bind but before the UDC=empty write).
	switch e := got.(type) {
	case usbip.Event:
		if bound, okBound := e.(domain.DeviceBoundEvent); okBound {
			require.Equal(t, busID, bound.Device.BusID)
		}

		if unbound, okUnbound := e.(domain.DeviceUnboundEvent); okUnbound {
			require.Equal(t, busID, unbound.Device.BusID)
		}
	default:
		t.Fatalf("unexpected event type: %T", got)
	}
}

// TestEventsPortAttachedDetached exercises the uevent fan-out's
// PortAttachedEvent / PortDetachedEvent production on the VUDC
// mass-storage loopback. Unlike an arbitrary physical device, this fixture
// is known to enumerate successfully through vhci_hcd, so its USB add/remove
// uevents are deterministic evidence for the public Watch contract.
func TestEventsPortAttachedDetached(t *testing.T) {
	vudc := integration.SetupVUDCWithData(t, deterministicPayload(e2ePayloadSize))
	skipIfVUDCExportUnavailable(t, vudc.BusID)
	waitVUDCAvailable(t, vudc.BusID)

	ctx, cancel := context.WithTimeout(context.Background(), eventScenarioDeadline)
	defer cancel()

	lis, addr, err := integration.TCPListen(loopbackExporterAddr)
	require.NoError(t, err)

	serveDone := make(chan error, 1)

	go func() {
		serveDone <- serveVUDCSocket(lis, vudcRemoteBusID, vudc.BusID)
	}()

	t.Cleanup(func() { _ = lis.Close() })

	imp, err := usbip.NewImporter()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	// Watch is intentionally lazy: its netlink subscription starts when the
	// iterator is consumed, not when Watch returns. Keep one consumer alive
	// for the complete attach/detach lifecycle. Do not retry Attach while the
	// exporter still owns the first session; doing so tests the busy-device
	// rejection path and can obscure the detach event this scenario needs to
	// observe.
	var eventsMu sync.Mutex

	attachedIDs := make(map[domain.PortID]bool)
	detachedIDs := make(map[domain.PortID]bool)
	watchReady := make(chan struct{})

	var watchReadyOnce sync.Once

	go imp.Watch(ctx)(func(event usbip.Event) bool {
		eventsMu.Lock()
		defer eventsMu.Unlock()

		switch typed := event.(type) {
		case domain.DeviceBoundEvent:
			if typed.Device.BusID == domain.BusID(vudc.BusID) {
				watchReadyOnce.Do(func() { close(watchReady) })
			}
		case domain.PortAttachedEvent:
			attachedIDs[typed.Port.ID] = true
		case domain.PortDetachedEvent:
			detachedIDs[typed.Port.ID] = true
		}

		return ctx.Err() == nil
	})

	observedEvent := func(events map[domain.PortID]bool, id domain.PortID) bool {
		eventsMu.Lock()
		defer eventsMu.Unlock()

		return events[id]
	}

	// Establish a deterministic subscription barrier before Attach. Watch is
	// lazy, so merely starting its goroutine does not prove the netlink socket
	// has subscribed. Trigger a harmless change uevent on the already-bound
	// VUDC until the same Watch consumer observes it; only then can the real
	// VHCI attach event be generated without racing subscription startup.
	ueventPath := filepath.Join(eventUDCClassRoot, vudc.BusID, eventUeventAttribute)

	var triggerErr error

	require.Eventually(t, func() bool {
		select {
		case <-watchReady:
			return true
		default:
		}

		triggerErr = os.WriteFile(
			ueventPath,
			[]byte(eventUeventChange),
			eventSysfsWriteMode,
		)

		return false
	}, eventCollectDeadline, eventRetryInterval,
		"Watch subscription must observe the VUDC readiness uevent (last trigger error: %v)",
		triggerErr)
	require.NoError(t, triggerErr)

	attachStarted := time.Now()

	port, err := imp.Attach(
		ctx,
		domain.RemoteEndpoint{Host: addr.Host, Port: addr.Port},
		vudcRemoteBusID,
		usbip.AttachOptions{},
	)
	require.NoError(t, err)

	select {
	case serveErr := <-serveDone:
		require.NoError(t, serveErr, "server-side VUDC fd handoff")
	case <-ctx.Done():
		t.Fatalf("server-side VUDC fd handoff: %v", ctx.Err())
	}

	// PortAttached is emitted by USB-core enumeration, not by the VHCI
	// attach sysfs write itself. Wait until the mass-storage fixture is
	// fully usable before detaching so the test cannot tear enumeration
	// down midway and intermittently lose the corresponding remove event.
	blockDev := waitForVHCIBlockDevice(
		t,
		eventScenarioDeadline,
		attachStarted,
		e2ePayloadSize,
	)

	require.Eventually(t, func() bool {
		return observedEvent(attachedIDs, port.ID)
	}, eventCollectDeadline, eventRetryInterval,
		"must observe PortAttachedEvent for port %d", port.ID)

	require.NoError(t, imp.Detach(ctx, port.ID))
	settleAfterDetach(t, vudc.BusID, blockDev)

	require.Eventually(t, func() bool {
		return observedEvent(detachedIDs, port.ID)
	}, eventCollectDeadline, eventRetryInterval,
		"must observe PortDetachedEvent for port %d", port.ID)
}

// awaitDeviceEvent consumes ch until a DeviceBoundEvent or
// DeviceUnboundEvent for busID is observed or ctx is cancelled. The
// second return value is false on cancellation or channel close.
func awaitDeviceEvent(ctx context.Context, ch iter.Seq[usbip.Event], busID domain.BusID) (usbip.Event, bool) {
	var out usbip.Event

	found := false

	ch(func(ev usbip.Event) bool {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		switch e := ev.(type) {
		case domain.DeviceBoundEvent:
			if e.Device.BusID == busID {
				out = e
				found = true

				return false
			}
		case domain.DeviceUnboundEvent:
			if e.Device.BusID == busID {
				out = e
				found = true

				return false
			}
		}

		return true
	})

	return out, found
}
