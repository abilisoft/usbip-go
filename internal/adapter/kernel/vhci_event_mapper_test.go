// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"errors"
	"testing"
	"testing/fstest"

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
		"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb1/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "5000\n",
	})
}

// dualControllerTopoFS fixtures two controllers at default HCPorts=8,
// VHCIPorts=16. BusMap = {1→(0,HS), 2→(0,SS), 3→(1,HS), 4→(1,SS)}.
func dualControllerTopoFS() fstest.MapFS {
	return topoFS(map[string]string{
		"/sys/devices/platform/vhci_hcd.0/nports":      "32\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/status.1":    "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb1/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "5000\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/busnum": "3\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/busnum": "4\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/speed":  "5000\n",
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
		"ACTION":    "remove",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-3",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "HS remove on controller 0 must produce an event")

	detach, isDetach := ev.(domain.PortDetachedEvent)
	require.True(t, isDetach, "expected PortDetachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(2), detach.Port.ID,
		"rhport0=2 on HS hub of controller 0 must flatten to Port.ID=2")
	require.Equal(t, domain.BusID("1-3"), detach.Port.BusID,
		"BusID must preserve the full matched busid for correlation")
}

// TestVhciEventMapper_SingleControllerSS covers ACTION=add on the SS hub.
// SS hub offset = HCPorts = 8; rhport0 = 0; flat Port.ID = 0*16 + 8 + 0
// = 8. BusMap places usbBus=2 at (ControllerIdx=0, Hub=SS).
func TestVhciEventMapper_SingleControllerSS(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		"ACTION":    "add",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb2/2-1",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "SS add on controller 0 must produce an event")

	attach, isAttach := ev.(domain.PortAttachedEvent)
	require.True(t, isAttach, "expected PortAttachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(8), attach.Port.ID,
		"rhport0=0 on SS hub of controller 0 must flatten to Port.ID=8")
	require.Equal(t, domain.BusID("2-1"), attach.Port.BusID)
}

// TestVhciEventMapper_MultiControllerHS covers ACTION=remove on
// controller 1's HS hub. usbBus=3 → (ControllerIdx=1, Hub=HS); rhport0
// = 2 - 1 = 1; flat Port.ID = 1*16 + 0 + 1 = 17.
func TestVhciEventMapper_MultiControllerHS(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, dualControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		"ACTION":    "remove",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.1/usb3/3-2",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "HS remove on controller 1 must produce an event")

	detach, isDetach := ev.(domain.PortDetachedEvent)
	require.True(t, isDetach, "expected PortDetachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(17), detach.Port.ID,
		"rhport0=1 on HS hub of controller 1 must flatten to Port.ID=17")
	require.Equal(t, domain.BusID("3-2"), detach.Port.BusID)
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
		"ACTION":    "change",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.1/usb4/4-3",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "SS change on controller 1 must produce an event")

	errEv, isErr := ev.(domain.PortErroredEvent)
	require.True(t, isErr, "expected PortErroredEvent, got %T", ev)
	require.Equal(t, domain.PortID(26), errEv.Port.ID,
		"rhport0=2 on SS hub of controller 1 must flatten to Port.ID=26")
	require.Equal(t, domain.BusID("4-3"), errEv.Port.BusID)
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
		"ACTION":    "add",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb99/99-1",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.False(t, ok, "bus 99 is not in the BusMap — mapper must skip")
	require.Nil(t, ev, "skipped events must have nil payload")
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
		{name: "not a vhci devpath", devpath: "/devices/pci0000:00/0000:00:14.0/usb1/1-1"},
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
				"ACTION":    "add",
				"SUBSYSTEM": "usb",
				"DEVPATH":   tc.devpath,
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
		"/sys/devices/platform/vhci_hcd.0/nports":      "16\n",
		"/sys/devices/platform/vhci_hcd.0/status":      "",
		"/sys/devices/platform/vhci_hcd.0/status.1":    "",
		"/sys/devices/platform/vhci_hcd.0/usb1/busnum": "1\n",
		"/sys/devices/platform/vhci_hcd.0/usb1/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/busnum": "2\n",
		"/sys/devices/platform/vhci_hcd.0/usb2/speed":  "5000\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/busnum": "3\n",
		"/sys/devices/platform/vhci_hcd.1/usb3/speed":  "480\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/busnum": "4\n",
		"/sys/devices/platform/vhci_hcd.1/usb4/speed":  "5000\n",
	})

	topo := loadTopoForMapperTest(t, mfs)
	require.EqualValues(t, 4, topo.HCPorts, "fixture sanity: HCPorts must be 4")
	require.EqualValues(t, 8, topo.VHCIPorts, "fixture sanity: VHCIPorts must be 8")

	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		"ACTION":    "add",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb2/2-1",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok)

	attach, isAttach := ev.(domain.PortAttachedEvent)
	require.True(t, isAttach, "expected PortAttachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(4), attach.Port.ID,
		"rhport0=0 on SS hub of controller 0 with HCPorts=4 must flatten to Port.ID=4")
}

// TestVhciEventMapper_DottedBusIDProducesFlatPort pins hub-chained
// busid resolution. The full busid "1-2.3" preserves as domain.BusID,
// but the flat Port.ID only indexes the root-hub port (rhport0=1).
// HS offset = 0; controller 0 offset = 0; flat Port.ID = 0*16 + 0 + 1
// = 1.
func TestVhciEventMapper_DottedBusIDProducesFlatPort(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		"ACTION":    "remove",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-2.3",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "dotted busid must still map to a flat port")

	detach, isDetach := ev.(domain.PortDetachedEvent)
	require.True(t, isDetach, "expected PortDetachedEvent, got %T", ev)
	require.Equal(t, domain.PortID(1), detach.Port.ID,
		"rhport0 is always the leading segment before the first '.'")
	require.Equal(t, domain.BusID("1-2.3"), detach.Port.BusID,
		"full dotted busid preserved verbatim in the emitted event")
}

// TestVhciEventMapper_AnchoredRegexPreservesValidBusIDs is the positive
// counterpart to the unanchored-regex guard in the Malformed table. It
// pins that the end-anchored vhci devpath regex still accepts the full
// range of VALID devpath shapes the kernel emits on root-hub-level
// add/remove events: single-digit root port, dotted hub-attached busid
// (1-1.2), and deeper chains (1-1.2.3). Each case maps to a
// PortDetachedEvent whose Port.BusID preserves the full dotted path.
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
			devpath: "/devices/platform/vhci_hcd.0/usb1/1-1",
			busID:   "1-1",
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
				"ACTION":    "remove",
				"SUBSYSTEM": "usb",
				"DEVPATH":   tc.devpath,
			}

			ev, ok := mapper.MapEventForTest(fields)
			require.True(t, ok, "valid devpath %q must map", tc.devpath)

			detach, isDetach := ev.(domain.PortDetachedEvent)
			require.True(t, isDetach, "expected PortDetachedEvent, got %T", ev)
			require.Equal(t, tc.busID, detach.Port.BusID,
				"full dotted busid must be preserved verbatim")
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
//  3. The first VHCI-shaped event fires the loader exactly once. On
//     loader error, VHCI events are dropped (ok=false) — no panic, no
//     surfaced error; the mapper is degraded for VHCI only.
//  4. A loader-error result is memoised: subsequent VHCI events do not
//     re-invoke the loader.
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

	mapper := kernel.NewVHCIEventMapperWithLoaderForTest(loader)

	require.Zero(t, calls,
		"mapper construction must not call the topology loader — lazy init "+
			"keeps exporter-only deployments (no vhci_hcd) unaffected")

	// usbip_host event must NOT trigger the VHCI topology loader.
	hostFields := map[string]string{
		"ACTION":    "add",
		"SUBSYSTEM": "usbip_host",
		"DEVPATH":   "/devices/pci0000:00/0000:00:14.0/usb1/1-1",
	}

	hostEvent, hostOK := mapper.MapEventForTest(hostFields)
	require.True(t, hostOK,
		"usbip_host event must map even when the VHCI topology is absent")

	bound, isBound := hostEvent.(domain.DeviceBoundEvent)
	require.True(t, isBound, "expected DeviceBoundEvent, got %T", hostEvent)
	require.Equal(t, domain.BusID("1-1"), bound.Device.BusID)
	require.Zero(t, calls,
		"usbip_host events must bypass the VHCI topology entirely — "+
			"the loader must still not have been called")

	// First VHCI-shaped event triggers the loader exactly once; since
	// the loader errors, the VHCI event is dropped but no caller-
	// visible error is produced.
	vhciFields := map[string]string{
		"ACTION":    "remove",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-1",
	}

	vhciEvent, vhciOK := mapper.MapEventForTest(vhciFields)
	require.False(t, vhciOK,
		"VHCI event must be dropped when the topology loader fails — "+
			"the exporter-only deployment has no vhci_hcd module to query")
	require.Nil(t, vhciEvent, "dropped VHCI event must carry no payload")

	require.Equal(t, 1, calls,
		"the loader must be invoked exactly once on the first VHCI event")

	// Loader failures are not memoised — each subsequent VHCI event
	// retries so the mapper recovers automatically after a transient
	// sysfs error or vhci_hcd module reload.
	_, _ = mapper.MapEventForTest(vhciFields)

	require.Equal(t, 2, calls,
		"loader failure must not be memoised — second VHCI event must retry")
}

// TestVhciEventMapper_LazyLoaderSuccessCachedAcrossVHCIEvents mirrors
// the degraded test above but with a successful loader. Pins the
// "load once on first VHCI event, reuse thereafter" cache contract.
func TestVhciEventMapper_LazyLoaderSuccessCachedAcrossVHCIEvents(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())

	var calls int

	loader := func() (kernel.Topology, error) {
		calls++

		return topo, nil
	}

	mapper := kernel.NewVHCIEventMapperWithLoaderForTest(loader)

	require.Zero(t, calls, "construction must be lazy")

	vhciFields := map[string]string{
		"ACTION":    "remove",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-1",
	}

	_, ok := mapper.MapEventForTest(vhciFields)
	require.True(t, ok, "first VHCI event maps once the loader succeeds")
	require.Equal(t, 1, calls, "loader fires exactly once on first VHCI event")

	_, ok = mapper.MapEventForTest(vhciFields)
	require.True(t, ok)
	require.Equal(t, 1, calls, "loader success must be memoised")
}

// TestVhciEventMapper_UsbipHostPassThrough confirms the mapper does not
// interfere with SUBSYSTEM=usbip_host events — those are classified by
// the trailing busid segment and do not require topology. The mapper's
// MapEvent must route them through the non-vhci path unchanged.
func TestVhciEventMapper_UsbipHostPassThrough(t *testing.T) {
	t.Parallel()

	topo := loadTopoForMapperTest(t, singleControllerTopoFS())
	mapper := kernel.NewVHCIEventMapperForTest(topo)

	fields := map[string]string{
		"ACTION":    "add",
		"SUBSYSTEM": "usbip_host",
		"DEVPATH":   "/devices/pci0000:00/0000:00:14.0/usb1/1-1",
	}

	ev, ok := mapper.MapEventForTest(fields)
	require.True(t, ok, "usbip_host bind events must still map")

	bound, isBound := ev.(domain.DeviceBoundEvent)
	require.True(t, isBound, "expected DeviceBoundEvent, got %T", ev)
	require.Equal(t, domain.BusID("1-1"), bound.Device.BusID)
}
