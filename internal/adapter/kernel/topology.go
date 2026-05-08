// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// HubType distinguishes the two per-controller VHCI hubs: high-speed
// (USB 2.x, root hub registered first by vhci_hcd.c) and super-speed
// (USB 3.x, registered second). The kernel's flat port numbering places
// all HS ports of controller N before all SS ports of controller N.
type HubType uint8

const (
	// HubTypeHS is the high-speed (USB 2.x) hub registered first per
	// controller. Its hubOffset in the flat port space is 0.
	HubTypeHS HubType = iota
	// HubTypeSS is the SuperSpeed (USB 3.x) hub registered second per
	// controller. Its hubOffset in the flat port space is HCPorts.
	HubTypeSS
)

// VHCILocation identifies a (controller, hub) pair — the coordinates
// that together with an rhport (0-indexed, relative to the hub) uniquely
// locate a VHCI port in the kernel's flat port space.
type VHCILocation struct {
	// ControllerIdx is the vhci_hcd.<N> suffix.
	ControllerIdx uint32
	// Hub selects the HS or SS root hub on that controller.
	Hub HubType
}

// StatusTopology is the minimal VHCI topology the status-reading path
// (readStatusRows, ListPorts, findFreePort) needs: controller count and
// per-controller VHCI_PORTS stride only. It deliberately omits the
// BusMap — status reading never consumes usb*/busnum, so requiring
// complete usb* children here would hard-fail ListPorts during live-
// host mid-probe races that the parser is otherwise equipped to
// handle. BusMap-dependent paths (uevent mapping) consume the full
// Topology instead.
type StatusTopology struct {
	// NControllers is the count of vhci_hcd.<N> platform devices.
	NControllers uint32
	// HCPorts is VHCI_HC_PORTS from the kernel's perspective — the
	// per-hub port count. Derived as nports/(nControllers*2).
	HCPorts uint32
	// VHCIPorts is the per-controller total (HCPorts*2, one HS hub +
	// one SS hub). Guaranteed nonzero on any StatusTopology returned
	// successfully by discoverStatusTopology.
	VHCIPorts uint32
}

// Topology is the read-once, cached snapshot of the VHCI sysfs
// topology. All BusMap-consuming code paths (uevent mapping, future
// port-to-bus translation) consume this snapshot rather than re-reading
// sysfs on every call. The status-reading path uses the lighter
// StatusTopology which omits BusMap.
type Topology struct {
	// NControllers is the count of vhci_hcd.<N> platform devices.
	NControllers uint32
	// HCPorts is VHCI_HC_PORTS from the kernel's perspective — the
	// per-hub port count. Derived as nports/(nControllers*2).
	HCPorts uint32
	// VHCIPorts is the per-controller total (HCPorts*2, one HS hub +
	// one SS hub).
	VHCIPorts uint32
	// BusMap maps a Linux USB bus number (as reported by
	// usbN/busnum) to the VHCI location (controller + hub) backing
	// that bus.
	BusMap map[uint32]VHCILocation
}

// Status returns the BusMap-free projection of t. Used by callers that
// have a full Topology in hand but want to pass a StatusTopology to
// the status-reading helpers.
func (t Topology) Status() StatusTopology {
	return StatusTopology{
		NControllers: t.NControllers,
		HCPorts:      t.HCPorts,
		VHCIPorts:    t.VHCIPorts,
	}
}

// FlatPort converts a (location, rhport0) pair into the flat port
// identifier the kernel emits in the status file and expects on attach
// writes. rhport0 is 0-indexed and ranges over [0, HCPorts).
//
// Formula matches status_show_vhci in vhci_sysfs.c:
//
//	flat = pdev_nr * VHCI_PORTS + hubOffset + rhport
//	hubOffset = 0 for HS, VHCI_HC_PORTS for SS
func (t Topology) FlatPort(loc VHCILocation, rhport0 uint32) domain.PortID {
	return domain.PortID(loc.ControllerIdx*t.VHCIPorts + hubOffset(t, loc.Hub) + rhport0)
}

// hubOffset returns the per-hub offset inside a single controller's
// port block: HS hub starts at 0, SS hub starts at HCPorts.
func hubOffset(t Topology, h HubType) uint32 {
	if h == HubTypeSS {
		return t.HCPorts
	}

	return 0
}

// errTopologyNoControllers surfaces when probing status/status.N finds
// zero controllers — the vhci_hcd module is not loaded or its sysfs
// group is malformed.
var errTopologyNoControllers = errors.New("vhci topology: no controllers found")

// errTopologyInconsistent surfaces when nports is not divisible by
// 2*nControllers; the sysfs state is inconsistent and any downstream
// port arithmetic would be wrong.
var errTopologyInconsistent = errors.New("vhci topology: nports not divisible by 2*nControllers")

// errTopologyZeroNPorts surfaces when nports reads as zero. A Topology
// with VHCIPorts=0 panics the row-parser's controller-block check
// (port / vhciPorts) with integer divide by zero, so the snapshot is
// refused at discovery time rather than handed downstream.
var errTopologyZeroNPorts = errors.New("vhci topology: nports is zero")

// errTopologyIncomplete surfaces when one or more vhci_hcd.<N>
// controllers exposes fewer than hubsPerController usb children. A
// partial BusMap would silently misroute every flat-port lookup keyed
// on an absent bus, so we refuse the snapshot instead of accepting
// truncation.
var errTopologyIncomplete = errors.New("vhci topology: controller missing one or more usb child hubs")

// vhciHCDPlatformFmt is the format for the per-controller platform
// device directory. Controller 0 is "vhci_hcd.0", controller N is
// "vhci_hcd.<N>".
const vhciHCDPlatformFmt = "/sys/devices/platform/vhci_hcd.%d"

// Sysfs attribute names inside a usbN child directory of the vhci_hcd
// platform device. Classification is driven by sibling order
// (lowest-busnum child is HS, next is SS) so the speed attribute is
// intentionally not consulted — it is absent or unparseable in several
// live-host scenarios and sibling order matches vhci_hcd_probe's root
// hub registration sequence without exception.
const (
	usbChildBusnum = "busnum"
	usbChildPrefix = "usb"
)

// hubsPerController is the number of root hubs vhci_hcd registers per
// controller: exactly two — one HS hub, one SS hub — per vhci_hcd.c's
// hcd_name_hs / hcd_name_ss pair in add_platform_device. The constant
// is named rather than embedded as a literal so the algebra
// (VHCIPorts = HCPorts * hubsPerController, divisor = nControllers *
// hubsPerController) reads self-documenting.
const hubsPerController = 2

// discoverStatusTopology reads the status-reading slice of the vhci_hcd
// topology from fsys: nports + controller count, producing only
// NControllers / HCPorts / VHCIPorts. It does NOT walk usb* children,
// so a controller that is mid-probe or has a missing sibling hub still
// produces a usable snapshot for the status-reading path. fsys must be
// rooted at "/" (os.DirFS("/") in production, MapFS in tests). Errors:
// missing nports, zero or inconsistent nports, or zero controllers
// discovered.
func discoverStatusTopology(fsys fs.FS) (StatusTopology, error) {
	nports, err := ReadUint(fsys, path.Join(SysfsVHCIHCD, SysfsVHCINPorts))
	if err != nil {
		return StatusTopology{}, err
	}

	nControllers, err := probeControllerCount(fsys)
	if err != nil {
		return StatusTopology{}, err
	}

	hcPorts, err := deriveHCPorts(nports, nControllers)
	if err != nil {
		return StatusTopology{}, err
	}

	return StatusTopology{
		NControllers: nControllers,
		HCPorts:      hcPorts,
		VHCIPorts:    hcPorts * hubsPerController,
	}, nil
}

// discoverTopology reads the full vhci_hcd topology from fsys,
// including the BusMap produced by walking each controller's usb*
// children. fsys must be rooted at "/" (e.g. os.DirFS("/") in
// production, a MapFS in tests). Errors: any status-layer failure,
// any controller failing the len(BusMap) == nControllers *
// hubsPerController invariant. Status-reading callers should use
// discoverStatusTopology instead — this function's BusMap
// completeness check is irrelevant to row parsing and hard-fails
// live-host mid-probe races ListPorts would otherwise tolerate.
func discoverTopology(fsys fs.FS) (Topology, error) {
	status, err := discoverStatusTopology(fsys)
	if err != nil {
		return Topology{}, err
	}

	busMap, err := buildBusMap(fsys, status.NControllers)
	if err != nil {
		return Topology{}, err
	}

	expected := int(status.NControllers) * hubsPerController
	if len(busMap) != expected {
		return Topology{}, fmt.Errorf("%w: got %d bus entries, want %d (nControllers=%d)",
			errTopologyIncomplete, len(busMap), expected, status.NControllers)
	}

	return Topology{
		NControllers: status.NControllers,
		HCPorts:      status.HCPorts,
		VHCIPorts:    status.VHCIPorts,
		BusMap:       busMap,
	}, nil
}

// probeControllerCount walks vhci_hcd.0's status / status.N files until
// the first missing index. The kernel emits status for controller 0 and
// status.<i> for controllers 1..N-1, all rooted at vhci_hcd.0's sysfs
// group. A zero count indicates vhci_hcd is not loaded; any non-ENOENT
// open failure (EACCES, EIO, transient kernel error) propagates
// immediately — silently folding those into "present" would mask the
// real failure.
//
// No upper bound is imposed: ENOENT is the natural termination signal
// and always fires on the first unused index, so the loop runs in
// O(nControllers) and cannot exceed that regardless of deployment size.
func probeControllerCount(fsys fs.FS) (uint32, error) {
	var count uint32

	for i := uint32(0); ; i++ {
		exists, err := statusFileState(fsys, i)
		if err != nil {
			return 0, err
		}

		if !exists {
			break
		}

		count++
	}

	if count == 0 {
		return 0, errTopologyNoControllers
	}

	return count, nil
}

// statusFileState probes whether the per-controller status file for
// index i is present in fsys. The second return value is non-nil only
// when Open fails with an error that is *not* fs.ErrNotExist (e.g.
// permission denied, I/O error) — those must be surfaced verbatim so
// the caller sees a correctly-shaped failure instead of a silently
// truncated topology.
func statusFileState(fsys fs.FS, i uint32) (bool, error) {
	p := path.Join(SysfsVHCIHCD, statusFileName(i))

	f, err := fsys.Open(fsPathFromAbs(p))
	if err == nil {
		_ = f.Close()

		return true, nil
	}

	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	return false, classifyFSErr("open", p, err)
}

// deriveHCPorts enforces the sysfs invariant nports = nControllers *
// VHCI_PORTS = nControllers * VHCI_HC_PORTS * 2. nports=0 is surfaced
// with a dedicated sentinel because HCPorts=0 propagates to VHCIPorts=0
// and would panic any downstream `port / VHCIPorts` consumer; callers
// must see the specific failure, not a generic inconsistency error.
// If the division is inexact the kernel reported inconsistent state
// and we refuse to make up a value.
func deriveHCPorts(nports, nControllers uint32) (uint32, error) {
	if nports == 0 {
		return 0, fmt.Errorf("%w: nControllers=%d", errTopologyZeroNPorts, nControllers)
	}

	divisor := nControllers * hubsPerController
	if divisor == 0 || nports%divisor != 0 {
		return 0, fmt.Errorf("%w: nports=%d nControllers=%d", errTopologyInconsistent, nports, nControllers)
	}

	return nports / divisor, nil
}

// buildBusMap walks each vhci_hcd.<idx> directory and inspects its usb*
// children. Each child's busnum attribute names the Linux USB bus
// number; the speed attribute classifies the hub (HS vs SS). The
// resulting map keys every bus owned by VHCI and excludes buses owned
// by other HCDs.
func buildBusMap(fsys fs.FS, nControllers uint32) (map[uint32]VHCILocation, error) {
	busMap := make(map[uint32]VHCILocation, nControllers*2)

	for idx := range nControllers {
		err := appendControllerBusMap(fsys, idx, busMap)
		if err != nil {
			return nil, err
		}
	}

	return busMap, nil
}

// appendControllerBusMap lists the usb* child directories of a single
// vhci_hcd.<idx>, sorts them by busnum ascending, and folds each one
// into busMap. Classification is driven by sibling order — the first
// usb child (lowest busnum) is HS, the second is SS — because
// vhci_hcd_probe registers the HS root hub before the SS root hub for
// every controller. The speed attribute, when present, is a sanity
// signal only; empty or unparseable speed no longer corrupts the
// classification.
//
// Missing controller directories are tolerated silently here because
// probeControllerCount already validated status visibility; a race
// mid-scan against a vanishing platform device surfaces downstream at
// the first sysfs read.
func appendControllerBusMap(fsys fs.FS, idx uint32, busMap map[uint32]VHCILocation) error {
	ctrlDir := fmt.Sprintf(vhciHCDPlatformFmt, idx)

	entries, err := fs.ReadDir(fsys, fsPathFromAbs(ctrlDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: vhci_hcd.%d missing platform directory", errTopologyIncomplete, idx)
		}

		return classifyFSErr("readdir", ctrlDir, err)
	}

	children, err := collectUSBChildren(fsys, ctrlDir, entries)
	if err != nil {
		return err
	}

	if len(children) != hubsPerController {
		return fmt.Errorf("%w: vhci_hcd.%d has %d usb children (want %d)",
			errTopologyIncomplete, idx, len(children), hubsPerController)
	}

	sort.Slice(children, func(i, j int) bool {
		return children[i].busnum < children[j].busnum
	})

	for rank, ch := range children {
		busMap[ch.busnum] = VHCILocation{ControllerIdx: idx, Hub: hubByRank(rank)}
	}

	return nil
}

// usbChild captures the (busnum, directory-name) pair for a single
// usb* child inside a vhci_hcd.<idx> platform directory. The name is
// retained for diagnostics; classification needs only the busnum.
type usbChild struct {
	busnum uint32
	name   string
}

// collectUSBChildren walks entries and returns the usb* children with
// their parsed busnum values. Missing busnum attributes are tolerated
// (the live host may expose a partial child mid-probe); any other
// read/parse failure propagates.
func collectUSBChildren(fsys fs.FS, ctrlDir string, entries []fs.DirEntry) ([]usbChild, error) {
	children := make([]usbChild, 0, len(entries))

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), usbChildPrefix) {
			continue
		}

		busnumPath := path.Join(ctrlDir, e.Name(), usbChildBusnum)

		busnum, err := ReadUint(fsys, busnumPath)
		if err != nil {
			// classifyFSErr maps ENOENT under kindController to
			// ErrKernelModuleMissing — accept either form so the
			// "skip transient missing busnum" path holds whether the
			// underlying error surfaces as fs.ErrNotExist (test
			// fstest.MapFS) or the domain-classified wrapper (live
			// sysfs through unix.ENOENT).
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, domain.ErrKernelModuleMissing) {
				continue
			}

			return nil, err
		}

		children = append(children, usbChild{busnum: busnum, name: e.Name()})
	}

	return children, nil
}

// hubByRank maps sibling rank (0-based, ascending busnum) onto the
// HubType registered at that slot by vhci_hcd_probe: rank 0 → HS, rank
// 1 → SS. Any higher rank is treated as HS for now; a well-formed
// controller has exactly two usb children, and Bug 3's post-condition
// check catches deviations from that invariant.
func hubByRank(rank int) HubType {
	if rank == 1 {
		return HubTypeSS
	}

	return HubTypeHS
}

// loadTopology is the adapter-facing wrapper that reads the full
// BusMap-inclusive topology from the adapter's injected fs.FS and
// returns the memoised result on every subsequent call after the first
// success. Errors are not memoised — any transient sysfs failure is
// surfaced to the caller but leaves the cache empty, so the next call
// re-runs discoverTopology. This allows long-lived daemons to recover
// automatically after a vhci_hcd module reload.
//
// The cache is shared across every copy of commonAdapter because it is
// held through a pointer; see commonAdapter / topologyCache in
// adapter.go.
func (a *commonAdapter) loadTopology() (Topology, error) {
	a.topoCache.mu.Lock()
	defer a.topoCache.mu.Unlock()

	if a.topoCache.ok {
		return a.topoCache.topo, nil
	}

	topo, err := discoverTopology(a.fs)
	if err != nil {
		return Topology{}, err
	}

	a.topoCache.topo = topo
	a.topoCache.ok = true

	return topo, nil
}

// loadStatusTopology is the status-reading variant of loadTopology. It
// returns only NControllers / HCPorts / VHCIPorts — the fields
// readStatusRows / parseStatusFile need — and never asserts BusMap
// completeness. Cached independently from the full Topology so a
// transient BusMap shortfall (e.g. a controller mid-probe) does not
// poison the status-reading path. Errors are not memoised for the
// same retry-after-transient-failure reason as loadTopology.
func (a *commonAdapter) loadStatusTopology() (StatusTopology, error) {
	a.statusTopoCache.mu.Lock()
	defer a.statusTopoCache.mu.Unlock()

	if a.statusTopoCache.ok {
		return a.statusTopoCache.topo, nil
	}

	topo, err := discoverStatusTopology(a.fs)
	if err != nil {
		return StatusTopology{}, err
	}

	a.statusTopoCache.topo = topo
	a.statusTopoCache.ok = true

	return topo, nil
}
