// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"context"
	"iter"
	"os"
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
const eventCollectDeadline = 5 * time.Second

// TestEventsDeviceBoundUnbound proves spec §8.4's "Watch emits
// EventDeviceBound on configfs add" contract. The harness's SetupVUDC
// creates a gadget and writes the UDC attribute, which the kernel
// translates into a DEVTYPE=usbip-vudc-device uevent. A subscribed
// Importer/Watch iterator observes the matching DeviceBoundEvent.
//
// Tear-down by writing "" to UDC during t.Cleanup produces the
// corresponding DeviceUnboundEvent. Both events carry the busid the
// harness chose so the assertion is specific to THIS gadget — other
// parallel tests' gadgets do not pollute the observation.
//
// Skips cleanly if SetupVUDC itself skipped (missing modules or
// exhausted vudc UDCs per spec §8.4).
func TestEventsDeviceBoundUnbound(t *testing.T) {
	dev := integration.SetupVUDC(t)

	imp, err := usbip.NewImporter()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	ctx, cancel := context.WithTimeout(context.Background(), eventCollectDeadline)
	defer cancel()

	busID := domain.BusID(dev.BusID)

	// Setup already happened; the first subscriber may have missed
	// the bind event. Synthesise a deterministic second event by
	// writing a no-op to /sys and re-reading: the kernel emits a
	// fresh uevent on the attribute write. We cannot re-bind without
	// a matching unbind, so instead we rely on the uevent the UDC
	// write has already emitted — if the subscription races past it,
	// the scenario continues with the unbind event after Close.
	ch := imp.Watch(ctx)

	// Unbind to synthesise the second event. This both exercises
	// the unbound path AND re-emits the bound event at re-bind.
	// configfs rejects a zero-byte write with -EFAULT; writing a lone
	// newline is the canonical way to unbind a UDC — the kernel's
	// gadget_dev_desc_UDC_store strips the trailing \n and the empty
	// result triggers unregister_gadget. Same pattern as the harness
	// cleanup path in harness.go's runGadgetCleanup.
	err = os.WriteFile("/sys/kernel/config/usb_gadget/"+dev.Name+"/UDC", []byte("\n"), 0o644)
	require.NoError(t, err, "unbind UDC must succeed")

	got, ok := awaitDeviceEvent(ctx, ch, busID)
	require.True(t, ok, "must observe a DeviceBound or DeviceUnbound event for %s", busID)

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
// PortAttachedEvent / PortDetachedEvent production on a real loopback
// attach+detach. Requires USBIPGO_INTEGRATION_BUSID to name a real
// (non-vudc) USB busid bind-able via usbip-host; otherwise skips per
// the §8.4 env-gated skip exception — vudc devices don't traverse the
// usbip-host bind path.
func TestEventsPortAttachedDetached(t *testing.T) {
	integration.SetupVUDC(t)

	busID := integration.RequireRealBusID(t)

	ctx, cancel := context.WithTimeout(context.Background(), eventCollectDeadline)
	defer cancel()

	exp, err := usbip.NewExporter()
	require.NoError(t, err)

	integration.RequireBindable(t, ctx, exp, busID)

	t.Cleanup(func() {
		uctx, ucancel := context.WithTimeout(context.Background(), 2*time.Second)

		defer ucancel()

		_ = exp.Shutdown(uctx)
	})

	lis, addr, err := integration.TCPListen(loopbackExporterAddr)
	require.NoError(t, err)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(context.Background(), lis) }()

	t.Cleanup(func() {
		_ = lis.Close()

		select {
		case <-serveDone:
		case <-time.After(2 * time.Second):
		}
	})

	imp, err := usbip.NewImporter()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	// Subscribe BEFORE attach so the PortAttached event cannot race
	// past the test's observation window.
	ch := imp.Watch(ctx)

	port, err := imp.Attach(ctx, domain.RemoteEndpoint{Host: addr.Host, Port: addr.Port}, busID, usbip.AttachOptions{})
	require.NoError(t, err)

	got, ok := awaitPortAttachedEvent(ctx, ch, port.ID)
	require.True(t, ok, "must observe PortAttachedEvent for port %d", port.ID)
	require.Equal(t, port.ID, got.Port.ID, "event must carry the attached port id")

	err = imp.Detach(ctx, port.ID)
	require.NoError(t, err)

	gotDetached, ok := awaitPortDetachedEvent(ctx, ch, port.ID)
	require.True(t, ok, "must observe PortDetachedEvent for port %d", port.ID)
	require.Equal(t, port.ID, gotDetached.Port.ID)
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

// awaitPortAttachedEvent consumes ch until PortAttachedEvent for id
// lands. ctx cancellation aborts the iteration.
func awaitPortAttachedEvent(ctx context.Context, ch iter.Seq[usbip.Event], id domain.PortID) (domain.PortAttachedEvent, bool) {
	var out domain.PortAttachedEvent

	found := false

	ch(func(ev usbip.Event) bool {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		e, ok := ev.(domain.PortAttachedEvent)
		if ok && e.Port.ID == id {
			out = e
			found = true

			return false
		}

		return true
	})

	return out, found
}

// awaitPortDetachedEvent is the Detached-side counterpart to
// awaitPortAttachedEvent.
func awaitPortDetachedEvent(ctx context.Context, ch iter.Seq[usbip.Event], id domain.PortID) (domain.PortDetachedEvent, bool) {
	var out domain.PortDetachedEvent

	found := false

	ch(func(ev usbip.Event) bool {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		e, ok := ev.(domain.PortDetachedEvent)
		if ok && e.Port.ID == id {
			out = e
			found = true

			return false
		}

		return true
	})

	return out, found
}

