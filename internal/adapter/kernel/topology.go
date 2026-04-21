//go:build linux

package kernel

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
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

// Topology is the read-once, cached snapshot of the VHCI sysfs
// topology. All downstream VHCI port arithmetic (attach, detach, status
// row renumbering) consumes this snapshot rather than re-reading sysfs
// on every call.
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

// maxControllerProbe caps the status.N probe loop. The kernel's
// VHCI_NR_HCS default is 1 and is rarely reconfigured above single
// digits; capping at 16 is well above any realistic deployment and
// prevents unbounded directory scans on a malformed sysfs.
const maxControllerProbe = 16

// vhciHCDPlatformFmt is the format for the per-controller platform
// device directory. Controller 0 is "vhci_hcd.0", controller N is
// "vhci_hcd.<N>".
const vhciHCDPlatformFmt = "/sys/devices/platform/vhci_hcd.%d"

// Sysfs attribute names inside a usbN child directory of the vhci_hcd
// platform device.
const (
	usbChildBusnum = "busnum"
	usbChildSpeed  = "speed"
	usbChildPrefix = "usb"
)

// superSpeedThresholdMbps is the speed (in Mbps) at or above which a
// USB root hub is classified as SuperSpeed. USB 3.0 is 5000 Mbps; USB
// 2.0 high-speed is 480 Mbps.
const superSpeedThresholdMbps = 5000

// hubsPerController is the number of root hubs vhci_hcd registers per
// controller: exactly two — one HS hub, one SS hub — per vhci_hcd.c's
// hcd_name_hs / hcd_name_ss pair in add_platform_device. The constant
// is named rather than embedded as a literal so the algebra
// (VHCIPorts = HCPorts * hubsPerController, divisor = nControllers *
// hubsPerController) reads self-documenting.
const hubsPerController = 2

// discoverTopology reads the full vhci_hcd topology from fsys. fsys
// must be rooted at "/" (e.g. os.DirFS("/") in production, a MapFS in
// tests). Errors: missing nports, inconsistent nports, or zero
// controllers discovered.
func discoverTopology(fsys fs.FS) (Topology, error) {
	nports, err := ReadUint(fsys, path.Join(SysfsVHCIHCD, SysfsVHCINPorts))
	if err != nil {
		return Topology{}, err
	}

	nControllers, err := probeControllerCount(fsys)
	if err != nil {
		return Topology{}, err
	}

	hcPorts, err := deriveHCPorts(nports, nControllers)
	if err != nil {
		return Topology{}, err
	}

	busMap, err := buildBusMap(fsys, nControllers)
	if err != nil {
		return Topology{}, err
	}

	return Topology{
		NControllers: nControllers,
		HCPorts:      hcPorts,
		VHCIPorts:    hcPorts * hubsPerController,
		BusMap:       busMap,
	}, nil
}

// probeControllerCount walks vhci_hcd.0's status / status.N files until
// the first missing index. The kernel emits status for controller 0 and
// status.<i> for controllers 1..N-1, all rooted at vhci_hcd.0's sysfs
// group. A zero count indicates vhci_hcd is not loaded.
func probeControllerCount(fsys fs.FS) (uint32, error) {
	var count uint32

	for i := range uint32(maxControllerProbe) {
		if !statusFileExists(fsys, i) {
			break
		}

		count++
	}

	if count == 0 {
		return 0, errTopologyNoControllers
	}

	return count, nil
}

// statusFileExists reports whether the per-controller status file for
// index i is present in fsys. Only fs.ErrNotExist is treated as
// "absent"; any other open error propagates as "present" so we surface
// the underlying failure later during row parsing.
func statusFileExists(fsys fs.FS, i uint32) bool {
	p := path.Join(SysfsVHCIHCD, statusFileName(i))

	f, err := fsys.Open(fsPathFromAbs(p))
	if err == nil {
		_ = f.Close()

		return true
	}

	return !errors.Is(err, fs.ErrNotExist)
}

// deriveHCPorts enforces the sysfs invariant nports = nControllers *
// VHCI_PORTS = nControllers * VHCI_HC_PORTS * 2. If the division is
// inexact the kernel reported inconsistent state and we refuse to make
// up a value.
func deriveHCPorts(nports, nControllers uint32) (uint32, error) {
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
// vhci_hcd.<idx> and folds each one into busMap. Missing controller
// directories are tolerated silently — vhci_hcd.0 is guaranteed by
// probeControllerCount, but a race mid-scan could still race the
// probe; the adapter surfaces downstream module-missing errors on
// actual use.
func appendControllerBusMap(fsys fs.FS, idx uint32, busMap map[uint32]VHCILocation) error {
	ctrlDir := fmt.Sprintf(vhciHCDPlatformFmt, idx)

	entries, err := fs.ReadDir(fsys, fsPathFromAbs(ctrlDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return classifyFSErr("readdir", ctrlDir, err)
	}

	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), usbChildPrefix) {
			continue
		}

		derr := recordUSBChild(fsys, ctrlDir, e.Name(), idx, busMap)
		if derr != nil {
			return derr
		}
	}

	return nil
}

// recordUSBChild reads busnum and speed from a single usb<N> directory
// and folds the (bus, location) pair into busMap. Missing attributes
// are treated as a non-fatal skip: the live-host scenario sometimes
// exposes partial children while probe_disconnect is mid-flight.
func recordUSBChild(fsys fs.FS, ctrlDir, name string, idx uint32, busMap map[uint32]VHCILocation) error {
	busnumPath := path.Join(ctrlDir, name, usbChildBusnum)

	busnum, err := ReadUint(fsys, busnumPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		return err
	}

	hub, err := classifyHub(fsys, path.Join(ctrlDir, name))
	if err != nil {
		return err
	}

	busMap[busnum] = VHCILocation{ControllerIdx: idx, Hub: hub}

	return nil
}

// classifyHub reads the usb device's speed attribute and classifies the
// hub. Values at or above superSpeedThresholdMbps (5000 Mbps, USB 3.0)
// are SuperSpeed; everything else is high-speed. A missing speed
// attribute on a live VHCI root hub is unusual but tolerated by
// defaulting to HS (the registration order makes HS the first root hub
// per controller).
func classifyHub(fsys fs.FS, devDir string) (HubType, error) {
	speed, err := ReadUint(fsys, path.Join(devDir, usbChildSpeed))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return HubTypeHS, nil
		}

		return HubTypeHS, err
	}

	if speed >= superSpeedThresholdMbps {
		return HubTypeSS, nil
	}

	return HubTypeHS, nil
}

// loadTopology is the adapter-facing wrapper that reads the topology
// from the adapter's injected fs.FS. Task 2 and beyond consume this
// cached snapshot for every VHCI port calculation; re-reading on each
// call would race a live kernel's topology changes only in contrived
// tests.
func (a *commonAdapter) loadTopology() (Topology, error) {
	return discoverTopology(a.fs)
}
