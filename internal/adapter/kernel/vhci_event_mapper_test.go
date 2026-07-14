// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"errors"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// errMapperLoaderFailed is the canned error returned by the loader in
// the degraded-VHCI test below. Declared at package scope so the
// errors.Is assertion can reference the same sentinel.
var errMapperLoaderFailed = errors.New("mapper loader failed")

// singleControllerTopoFS is the canonical single-vhci_hcd fixture used
// by the mapper unit tests: one controller, HCPorts=8, VHCIPorts=16,
// BusMap = {1→(0,HS), 2→(0,SS)}.
func singleControllerTopoFS() fstest.MapFS {
	return topoFS(map[string]string{
		testVHCIController0NPortsPath:     testNPorts16Raw,
		testVHCIController0StatusPath:     "",
		testVHCIController0USB1BusNumPath: "1\n",
		testVHCIController0USB1SpeedPath:  testHighSpeedRaw,
		testVHCIController0USB2BusNumPath: "2\n",
		testVHCIController0USB2SpeedPath:  testSuperSpeedRaw,
	})
}

// dualControllerTopoFS fixtures two controllers at default HCPorts=8,
// VHCIPorts=16. BusMap = {1→(0,HS), 2→(0,SS), 3→(1,HS), 4→(1,SS)}.
func dualControllerTopoFS() fstest.MapFS {
	return topoFS(map[string]string{
		testVHCIController0NPortsPath:     testNPorts32Raw,
		testVHCIController0StatusPath:     "",
		testVHCIController0Status1Path:    "",
		testVHCIController0USB1BusNumPath: "1\n",
		testVHCIController0USB1SpeedPath:  testHighSpeedRaw,
		testVHCIController0USB2BusNumPath: "2\n",
		testVHCIController0USB2SpeedPath:  testSuperSpeedRaw,
		testVHCIController1USB3BusNumPath: "3\n",
		testVHCIController1USB3SpeedPath:  testHighSpeedRaw,
		testVHCIController1USB4BusNumPath: "4\n",
		testVHCIController1USB4SpeedPath:  testSuperSpeedRaw,
	})
}

// loadTopoForMapperTest builds a Topology from fsys via the exported
// white-box helper so each mapper test can drive the mapper against a
// realistic sysfs fixture without wiring an adapter.
func loadTopoForMapperTest(t *testing.T, fsys fstest.MapFS) kernel.Topology {
	t.Helper()

	topo, err := kernel.DiscoverTopologyForTest(fsys)
	require.NoError(t, err)

	return topo
}

// TestVhciEventMapper_SingleControllerHS covers the default ACTION=remove
// on the HS hub of controller 0. The devpath resolves to usbBus=1 in the
// BusMap, which the fixture pins to (ControllerIdx=0, Hub=HS). rhport0
// = rootPort1indexed - 1 = 2; HS hub offset = 0; controller offset = 0;
// flat Port.ID = 0*16 + 0 + 2 = 2.
func TestVhciEventMapper_SingleControllerHS(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   "/devices/platform/vhci_hcd.0/usb1/1-3",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "HS remove on controller 0 must produce an event")

	detach, isDetach := ev.(domain.PortDetachedEvent)
	require.True(t, isDetach, "expected PortDetachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(2), detach.Port.ID,
		"rhport0=2 on HS hub of controller 0 must flatten to Port.ID=2")
	require.Empty(t, detach.Port.BusID,
		"VHCI uevents do not contain the exporter's remote busid")
	require.Equal(t, domain.BusID("1-3"), detach.Port.LocalBusID,
		"LocalBusID must preserve the full VHCI-side busid for correlation")
}

// TestVhciEventMapper_SingleControllerSS covers ACTION=add on the SS hub.
// SS hub offset = HCPorts = 8; rhport0 = 0; flat Port.ID = 0*16 + 8 + 0
// = 8. BusMap places usbBus=2 at (ControllerIdx=0, Hub=SS).
func TestVhciEventMapper_SingleControllerSS(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		testUeventActionField:    testUeventActionAdd,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   "/devices/platform/vhci_hcd.0/usb2/2-1",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "SS add on controller 0 must produce an event")

	attach, isAttach := ev.(domain.PortAttachedEvent)
	require.True(t, isAttach, "expected PortAttachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(8), attach.Port.ID,
		"rhport0=0 on SS hub of controller 0 must flatten to Port.ID=8")
	require.Empty(t, attach.Port.BusID)
	require.Equal(t, domain.BusID("2-1"), attach.Port.LocalBusID)
}

// TestVhciEventMapper_MultiControllerHS covers ACTION=remove on
// controller 1's HS hub. usbBus=3 → (ControllerIdx=1, Hub=HS); rhport0
// = 2 - 1 = 1; flat Port.ID = 1*16 + 0 + 1 = 17.
func TestVhciEventMapper_MultiControllerHS(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, dualControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   "/devices/platform/vhci_hcd.1/usb3/3-2",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "HS remove on controller 1 must produce an event")

	detach, isDetach := ev.(domain.PortDetachedEvent)
	require.True(t, isDetach, "expected PortDetachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(17), detach.Port.ID,
		"rhport0=1 on HS hub of controller 1 must flatten to Port.ID=17")
	require.Empty(t, detach.Port.BusID)
	require.Equal(t, domain.BusID("3-2"), detach.Port.LocalBusID)
}

// TestVhciEventMapper_MultiControllerSS covers ACTION=change on
// controller 1's SS hub. usbBus=4 → (ControllerIdx=1, Hub=SS); rhport0
// = 3 - 1 = 2; SS offset = 8; flat Port.ID = 1*16 + 8 + 2 = 26.
// ACTION=change produces PortErroredEvent.
func TestVhciEventMapper_MultiControllerSS(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, dualControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		testUeventActionField:    "change",
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   "/devices/platform/vhci_hcd.1/usb4/4-3",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "SS change on controller 1 must produce an event")

	errEv, isErr := ev.(domain.PortErroredEvent)
	require.True(t, isErr, "expected PortErroredEvent, got %T", ev)
	require.Equal(t, domain.PortID(26), errEv.Port.ID,
		"rhport0=2 on SS hub of controller 1 must flatten to Port.ID=26")
	require.Empty(t, errEv.Port.BusID)
	require.Equal(t, domain.BusID("4-3"), errEv.Port.LocalBusID)
}

// TestVhciEventMapper_NonVHCIBusIgnored covers the defensive short-
// circuit: a uevent whose devpath references a USB bus number absent
// from the BusMap must be treated as "not ours" and dropped. usbBus=99
// is not in either fixture's map.
func TestVhciEventMapper_NonVHCIBusIgnored(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		testUeventActionField:    testUeventActionAdd,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   "/devices/platform/vhci_hcd.0/usb99/99-1",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.False(t, ok, "bus 99 is not in the BusMap — mapper must skip")
	require.Nil(t, ev, "skipped events must have nil payload")
}

// TestVhciEventMapper_RejectsCoordinatesOutsideFreshTopology proves the
// devpath's controller and root Port are validated against the freshly loaded
// BusMap location and HCPorts before FlatPort arithmetic.
func TestVhciEventMapper_RejectsCoordinatesOutsideFreshTopology(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	cases := []struct {
		name    string
		devpath string
	}{
		{
			name:    "controller suffix disagrees with bus map",
			devpath: "/devices/platform/vhci_hcd.1/usb1/1-1",
		},
		{
			name:    "root port exceeds fresh hub width",
			devpath: "/devices/platform/vhci_hcd.0/usb1/1-9",
		},
		{
			name:    "controller exceeds uint32 topology coordinate",
			devpath: "/devices/platform/vhci_hcd.4294967296/usb1/1-1",
		},
		{
			name:    "controller exceeds parser width",
			devpath: "/devices/platform/vhci_hcd.18446744073709551616/usb1/1-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ev, ok := mapper.MapEventForTest(map[string]string{
				testUeventActionField:    testUeventActionRemove,
				testUeventSubsystemField: testUeventSubsystemUSB,
				testUeventDevPathField:   tc.devpath,
			})
			require.False(t, ok)
			require.Nil(t, ev)
		})
	}
}

// TestVhciEventMapper_MalformedDevpath covers the parse guards: non-
// matching devpaths, rootPort=0 (1-indexed minimum is 1), non-numeric
// segments, and the non-vhci devpath prefix all must return ok=false.
func TestVhciEventMapper_MalformedDevpath(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	cases := []struct {
		name    string
		devpath string
	}{
		{name: "root port zero", devpath: "/devices/platform/vhci_hcd.0/usb1/1-0"},
		{name: "not a vhci devpath", devpath: testPhysicalUSBDeviceDevPath},
		{name: "no busid segment", devpath: "/devices/platform/vhci_hcd.0/usb1"},
		{name: "non numeric bus", devpath: "/devices/platform/vhci_hcd.0/usbfoo/foo-1"},
		// Unanchored-regex safeguard: a USB interface sub-path must not
		// match the VHCI pattern. The regex trailing "$" anchor prevents
		// the outer FindStringSubmatch from truncating an interface-
		// shaped devpath down to the parent busid and emitting a spurious
		// PortDetachedEvent on ACTION=remove.
		{
			name:    "usb interface sub-path",
			devpath: "/devices/platform/vhci_hcd.0/usb1/1-1/1-1:1.0",
		},
		{
			name:    "usb endpoint sub-path",
			devpath: "/devices/platform/vhci_hcd.0/usb1/1-1/1-1:1.0/ep_81",
		},
		{
			name:    "dotted busid with interface",
			devpath: "/devices/platform/vhci_hcd.0/usb1/1-1.2:1.0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fields := map[string]string{
				testUeventActionField:    testUeventActionAdd,
				testUeventSubsystemField: testUeventSubsystemUSB,
				testUeventDevPathField:   tc.devpath,
			}

			ev, ok := mapper.MapEventForTest(fields)
			require.False(t, ok, "malformed devpath %q must not map", tc.devpath)
			require.Nil(t, ev)
		})
	}
}

// TestVhciEventMapper_NonDefaultHCPorts covers a kernel built with
// VHCI_HC_PORTS=4 and two controllers: nports=16, HCPorts=4,
// VHCIPorts=8. An ACTION=add on controller 0 SS with rhport0=0 must
// flatten to 0*8 + 4 + 0 = 4.
func TestVhciEventMapper_NonDefaultHCPorts(t *testing.T) {
	t.Parallel()

	mfs := topoFS(map[string]string{
		testVHCIController0NPortsPath:     testNPorts16Raw,
		testVHCIController0StatusPath:     "",
		testVHCIController0Status1Path:    "",
		testVHCIController0USB1BusNumPath: "1\n",
		testVHCIController0USB1SpeedPath:  testHighSpeedRaw,
		testVHCIController0USB2BusNumPath: "2\n",
		testVHCIController0USB2SpeedPath:  testSuperSpeedRaw,
		testVHCIController1USB3BusNumPath: "3\n",
		testVHCIController1USB3SpeedPath:  testHighSpeedRaw,
		testVHCIController1USB4BusNumPath: "4\n",
		testVHCIController1USB4SpeedPath:  testSuperSpeedRaw,
	})

	topo := loadTopoForMapperTest(t, mfs)
	require.EqualValues(t, 4, topo.HCPorts, "fixture sanity: HCPorts must be 4")
	require.EqualValues(t, 8, topo.VHCIPorts, "fixture sanity: VHCIPorts must be 8")

	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		testUeventActionField:    testUeventActionAdd,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   "/devices/platform/vhci_hcd.0/usb2/2-1",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok)

	attach, isAttach := ev.(domain.PortAttachedEvent)
	require.True(t, isAttach, "expected PortAttachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(4), attach.Port.ID,
		"rhport0=0 on SS hub of controller 0 with HCPorts=4 must flatten to Port.ID=4")
}

// TestVhciEventMapper_DottedBusIDProducesFlatPort pins hub-chained
// busid resolution. The full busid "1-2.3" preserves as LocalBusID,
// but the flat Port.ID only indexes the root-hub port (rhport0=1).
// HS offset = 0; controller 0 offset = 0; flat Port.ID = 0*16 + 0 + 1
// = 1.
func TestVhciEventMapper_DottedBusIDProducesFlatPort(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   "/devices/platform/vhci_hcd.0/usb1/1-2.3",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "dotted busid must still map to a flat port")

	detach, isDetach := ev.(domain.PortDetachedEvent)
	require.True(t, isDetach, "expected PortDetachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(1), detach.Port.ID,
		"rhport0 is always the leading segment before the first '.'")
	require.Empty(t, detach.Port.BusID,
		"VHCI uevents do not contain the exporter's remote busid")
	require.Equal(t, domain.BusID("1-2.3"), detach.Port.LocalBusID,
		"full dotted local busid preserved verbatim in the emitted event")
}

// TestVhciEventMapper_AnchoredRegexPreservesValidBusIDs is the positive
// counterpart to the unanchored-regex guard in the Malformed table. It
// pins that the end-anchored vhci devpath regex still accepts the full
// range of VALID devpath shapes the kernel emits on root-hub-level
// add/remove events: single-digit root port, dotted hub-attached busid
// (1-1.2), and deeper chains (1-1.2.3). Each case maps to a
// PortDetachedEvent whose Port.LocalBusID preserves the full dotted path.
func TestVhciEventMapper_AnchoredRegexPreservesValidBusIDs(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	cases := []struct {
		name    string
		devpath string
		busID   domain.BusID
	}{
		{
			name:    "flat root-port busid",
			devpath: testVHCIDeviceDevPath,
			busID:   testRootBusID,
		},
		{
			name:    "dotted hub-attached busid",
			devpath: "/devices/platform/vhci_hcd.0/usb1/1-1.2",
			busID:   "1-1.2",
		},
		{
			name:    "deep hub chain busid",
			devpath: "/devices/platform/vhci_hcd.0/usb1/1-1.2.3",
			busID:   "1-1.2.3",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fields := map[string]string{
				testUeventActionField:    testUeventActionRemove,
				testUeventSubsystemField: testUeventSubsystemUSB,
				testUeventDevPathField:   tc.devpath,
			}

			ev, ok := mapper.MapEventForTest(fields)
			require.True(t, ok, "valid devpath %q must map", tc.devpath)

			detach, isDetach := ev.(domain.PortDetachedEvent)
			require.True(t, isDetach, "expected PortDetachedEvent, got %T", ev)
			require.Empty(t, detach.Port.BusID,
				"VHCI uevents do not contain the exporter's remote busid")
			require.Equal(t, tc.busID, detach.Port.LocalBusID,
				"full dotted local busid must be preserved verbatim")
		})
	}
}

// TestVhciEventMapper_LazyLoaderDegradesVHCIButPassesUsbipHost pins
// the combined lazy-loader contract:
//
//  1. Mapper construction MUST NOT invoke the topology loader. A
//     caller building the mapper during exporter-only Subscribe (no
//     vhci_hcd module loaded) cannot afford to pay a topology read;
//     the lazy init keeps that path unaffected.
//  2. usbip_host events MUST bypass the VHCI topology entirely — they
//     do not need it, and firing the loader on a usbip_host bind would
//     defeat point 1.
//  3. Each VHCI-shaped event fires the loader at most twice. On a
//     permanent loader error, VHCI events are dropped (ok=false) — no panic,
//     no surfaced error; the mapper is degraded for VHCI only.
//  4. Loader errors are not memoised: subsequent VHCI events retry.
//
// A loader that always fails exercises the degraded path; a usbip_host
// event issued alongside still maps. This is the exporter-only
// deployment contract: the netlink listener must keep delivering
// DeviceBoundEvent/DeviceUnboundEvent regardless of VHCI availability.
func TestVhciEventMapper_LazyLoaderDegradesVHCIButPassesUsbipHost(t *testing.T) {
	t.Parallel()

	var calls int

	loader := func() (kernel.Topology, error) {
		calls++

		return kernel.Topology{}, errMapperLoaderFailed
	}

	var waits []time.Duration

	mapper := kernel.NewVHCIEventMapperWithLoaderAndWaitForTest(
		loader,
		func(delay time.Duration) { waits = append(waits, delay) },
	)

	require.Zero(t, calls,
		"mapper construction must not call the topology loader — lazy init "+
			"keeps exporter-only deployments (no vhci_hcd) unaffected")

	// usbip-host driver event must NOT trigger the VHCI topology loader.
	hostFields := map[string]string{
		testUeventActionField:    testUeventActionBind,
		testUeventDriverField:    testUSBIPHostDriver,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   testPhysicalUSBDeviceDevPath,
	}

	hostEvent, hostOK := mapper.MapEventForTest(hostFields)
	require.True(t, hostOK,
		"usbip-host event must map even when the VHCI topology is absent")

	bound, isBound := hostEvent.(domain.DeviceBoundEvent)
	require.True(t, isBound, "expected DeviceBoundEvent, got %T", hostEvent)
	require.Equal(t, domain.BusID(testRootBusID), bound.Device.BusID)
	require.Zero(t, calls,
		"usbip-host events must bypass the VHCI topology entirely — "+
			"the loader must still not have been called")

	// First VHCI-shaped event exhausts its two bounded load attempts; since
	// both error, the VHCI event is dropped but no caller-visible error is
	// produced.
	vhciFields := map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   testVHCIDeviceDevPath,
	}

	vhciEvent, vhciOK := mapper.MapEventForTest(vhciFields)
	require.False(t, vhciOK,
		"VHCI event must be dropped when the topology loader fails — "+
			"the exporter-only deployment has no vhci_hcd module to query")
	require.Nil(t, vhciEvent, "dropped VHCI event must carry no payload")

	require.Equal(t, 2, calls,
		"the loader must be invoked twice before one VHCI event is dropped")
	require.Len(t, waits, 1, "one event gets exactly one bounded retry wait")
	require.Positive(t, waits[0], "the production retry delay must be non-zero")

	// Loader failures are not memoised — each subsequent VHCI event
	// retries so the mapper recovers automatically after a transient
	// sysfs error or vhci_hcd module reload.
	_, _ = mapper.MapEventForTest(vhciFields)

	require.Equal(t, 4, calls,
		"loader failure must not be memoised — second VHCI event gets fresh attempts")
	require.Len(t, waits, 2, "each failed event gets its own single retry wait")
}

// TestVhciEventMapper_RetriesTransientTopologyFailureForSameRemove proves a
// one-shot remove uevent is not lost when its first fresh sysfs snapshot fails.
// The injected wait records the bounded pause without sleeping in wall time.
func TestVhciEventMapper_RetriesTransientTopologyFailureForSameRemove(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	calls := 0

	loader := func() (kernel.Topology, error) {
		calls++
		if calls == 1 {
			return kernel.Topology{}, errMapperLoaderFailed
		}

		return topo, nil
	}

	var waits []time.Duration

	mapper := kernel.NewVHCIEventMapperWithLoaderAndWaitForTest(
		loader,
		func(delay time.Duration) { waits = append(waits, delay) },
	)

	event, ok := mapper.MapEventForTest(map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   testVHCIDeviceDevPath,
	})
	require.True(t, ok, "the same remove event must map after a transient first failure")
	require.Equal(t, 2, calls, "the one event receives exactly one fresh retry")
	require.Len(t, waits, 1, "retry uses the injected non-sleeping wait exactly once")
	require.Positive(t, waits[0], "the named production retry delay must be non-zero")

	detached, isDetached := event.(domain.PortDetachedEvent)
	require.True(t, isDetached, "expected exactly one PortDetachedEvent, got %T", event)
	require.Equal(t, domain.PortID(0), detached.Port.ID)
	require.Equal(t, domain.BusID(testRootBusID), detached.Port.LocalBusID)
}

// TestVhciEventMapper_RetriesFreshCoordinateMissForSameRemove covers the
// other retryable transient: a fresh topology snapshot that does not yet
// contain the event's bus coordinates, followed by the converged snapshot.
func TestVhciEventMapper_RetriesFreshCoordinateMissForSameRemove(t *testing.T) {
	t.Parallel()

	valid := loadTopoForMapperTest(t, singleControllerTopoFS())
	missing := valid

	missing.BusMap = map[uint32]kernel.VHCILocation{}

	topologies := []kernel.Topology{missing, valid}
	calls := 0
	waits := 0

	mapper := kernel.NewVHCIEventMapperWithLoaderAndWaitForTest(
		func() (kernel.Topology, error) {
			topo := topologies[calls]
			calls++

			return topo, nil
		},
		func(time.Duration) { waits++ },
	)

	event, ok := mapper.MapEventForTest(map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   testVHCIDeviceDevPath,
	})
	require.True(t, ok)
	require.Equal(t, 2, calls)
	require.Equal(t, 1, waits)

	detached, isDetached := event.(domain.PortDetachedEvent)
	require.True(t, isDetached)
	require.Equal(t, domain.PortID(0), detached.Port.ID)
	require.Equal(t, domain.BusID(testRootBusID), detached.Port.LocalBusID)
}

// TestVhciEventMapper_FreshTopologyAcrossVHCIEvents proves successful topology
// observations are event-local. The loader changes the same bus mapping between
// two events, and the second Port ID must reflect the new module generation.
func TestVhciEventMapper_FreshTopologyAcrossVHCIEvents(t *testing.T) {
	t.Parallel()

	topologies := []kernel.Topology{
		{
			NControllers: 1,
			HCPorts:      8,
			VHCIPorts:    16,
			BusMap: map[uint32]kernel.VHCILocation{
				1: {ControllerIdx: 0, Hub: kernel.HubTypeHS},
			},
		},
		{
			NControllers: 2,
			HCPorts:      4,
			VHCIPorts:    8,
			BusMap: map[uint32]kernel.VHCILocation{
				1: {ControllerIdx: 1, Hub: kernel.HubTypeSS},
			},
		},
	}

	var calls int

	loader := func() (kernel.Topology, error) {
		topo := topologies[calls]
		calls++

		return topo, nil
	}

	mapper := kernel.NewVHCIEventMapperWithLoaderForTest(loader)

	require.Zero(t, calls, "construction must be lazy")

	vhciFields := map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   testVHCIDeviceDevPath,
	}

	first, ok := mapper.MapEventForTest(vhciFields)
	require.True(t, ok, "first VHCI event maps once the loader succeeds")
	require.Equal(t, 1, calls, "loader fires exactly once on first VHCI event")

	firstDetach, ok := first.(domain.PortDetachedEvent)
	require.True(t, ok)
	require.Equal(t, domain.PortID(0), firstDetach.Port.ID)

	vhciFields[testUeventDevPathField] = "/devices/platform/vhci_hcd.1/usb1/1-1"

	second, ok := mapper.MapEventForTest(vhciFields)
	require.True(t, ok)
	require.Equal(t, 2, calls, "each VHCI event must discover a fresh topology")

	secondDetach, ok := second.(domain.PortDetachedEvent)
	require.True(t, ok)
	require.Equal(t, domain.PortID(12), secondDetach.Port.ID,
		"second event must use controller 1 SS mapping from reloaded topology")
}

// TestVhciEventMapper_DelayedEventFromPreviousControllerDroppedAfterReload
// models a queued controller-0 event arriving after bus 1 moved to controller 1.
// Fresh discovery must not reinterpret that stale coordinate as Port 12.
func TestVhciEventMapper_DelayedEventFromPreviousControllerDroppedAfterReload(t *testing.T) {
	t.Parallel()

	topo := kernel.Topology{
		NControllers: 2,
		HCPorts:      4,
		VHCIPorts:    8,
		BusMap: map[uint32]kernel.VHCILocation{
			1: {ControllerIdx: 1, Hub: kernel.HubTypeSS},
		},
	}
	mapper := kernel.NewVHCIEventMapperWithLoaderForTest(func() (kernel.Topology, error) {
		return topo, nil
	})

	ev, ok := mapper.MapEventForTest(map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   testVHCIDeviceDevPath,
	})
	require.False(t, ok)
	require.Nil(t, ev, "a delayed event must be dropped rather than remapped to the new controller")
}

// TestVhciEventMapper_ConcurrentFreshTopologyLoads exercises the event-local
// loader through concurrent callers. The loader intentionally uses an ordinary
// counter: resolveEventLocation's mutex must serialize access, and the race
// detector validates that future dispatcher fan-in cannot race loader state.
func TestVhciEventMapper_ConcurrentFreshTopologyLoads(t *testing.T) {
	t.Parallel()

	const events = 64

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	loads := 0
	mapper := kernel.NewVHCIEventMapperWithLoaderForTest(func() (kernel.Topology, error) {
		loads++

		return topo, nil
	})
	fields := map[string]string{
		testUeventActionField:    testUeventActionRemove,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   testVHCIDeviceDevPath,
	}

	var wg sync.WaitGroup

	results := make(chan bool, events)

	for range events {
		wg.Go(func() {
			_, ok := mapper.MapEventForTest(fields)
			results <- ok
		})
	}

	wg.Wait()
	close(results)

	for ok := range results {
		require.True(t, ok)
	}

	require.Equal(t, events, loads,
		"each concurrent VHCI event must receive one fresh serialized snapshot")
}

// TestVhciEventMapper_UsbipHostPassThrough confirms the mapper routes
// SUBSYSTEM=usb ACTION=bind DRIVER=usbip-host through the exporter path
// without requiring VHCI topology.
func TestVhciEventMapper_UsbipHostPassThrough(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		testUeventActionField:    testUeventActionBind,
		testUeventDriverField:    testUSBIPHostDriver,
		testUeventSubsystemField: testUeventSubsystemUSB,
		testUeventDevPathField:   testPhysicalUSBDeviceDevPath,
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "usbip-host bind events must still map")

	bound, isBound := ev.(domain.DeviceBoundEvent)
	require.True(t, isBound, "expected DeviceBoundEvent, got %T", ev)
	require.Equal(t, domain.BusID(testRootBusID), bound.Device.BusID)
}
