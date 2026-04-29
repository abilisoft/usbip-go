// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

// Sysfs path constants verified against drivers/usb/usbip/{stub_dev,
// stub_main,vhci_sysfs}.c in the upstream kernel. Every byte offset,
// path, and write payload that appears in other files of this package
// must route through a constant declared here so the kernel-interface
// contract lives in exactly one file.
//
// Absolute paths (rooted at "/sys/...") are the canonical spec values;
// at runtime the adapter resolves them relative to the injected fs.FS
// via pathFromAbs() below.
const (
	// SysfsUSBDevices is the USB subsystem device directory.
	SysfsUSBDevices = "/sys/bus/usb/devices"

	// SysfsUSBIPHostDriver is the usbip-host driver-level attribute root.
	SysfsUSBIPHostDriver = "/sys/bus/usb/drivers/usbip-host"

	// SysfsDriversProbe is the global USB-bus probe trigger. Writing a
	// busid forces the kernel to re-evaluate the driver match-table for
	// that device, attaching whichever native driver claims it. Used in
	// Bind's rollback path to restore the original driver after a bind
	// failure that left the bare device unbound.
	SysfsDriversProbe = "/sys/bus/usb/drivers_probe"

	// usbipHostDriverName is the basename of the usbip-host driver as it
	// appears in /sys/bus/usb/devices/<busid>/driver after bind. Used to
	// short-circuit a re-bind attempt when the device is already exported.
	usbipHostDriverName = "usbip-host"

	// SysfsDriverBind is the generic driver "bind" attribute filename.
	SysfsDriverBind = "bind"
	// SysfsDriverUnbind is the generic driver "unbind" attribute filename.
	SysfsDriverUnbind = "unbind"
	// SysfsMatchBusID is the usbip-host match_busid attribute filename.
	// Accepts "add <busid>" or "del <busid>".
	SysfsMatchBusID = "match_busid"
	// SysfsRebind is the usbip-host rebind attribute filename. Accepts
	// a bare "<busid>" to rebind to the original driver.
	SysfsRebind = "rebind"

	// SysfsUsbipSockfd is the per-device sockfd attribute. "<fd>" to
	// connect; "-1" to disconnect.
	SysfsUsbipSockfd = "usbip_sockfd"
	// SysfsUsbipStatus is the per-device status attribute.
	SysfsUsbipStatus = "usbip_status"

	// SysfsVHCIHCD is the vhci_hcd driver's sysfs group root. Even on
	// multi-controller kernels the group lives at vhci_hcd.0; additional
	// status files appear there as status.1, status.2, and so on.
	SysfsVHCIHCD = "/sys/devices/platform/vhci_hcd.0"
	// SysfsVHCIAttach is the vhci attach attribute filename. Write
	// "%u %d %u %u" = port sockfd devid speed.
	SysfsVHCIAttach = "attach"
	// SysfsVHCIDetach is the vhci detach attribute filename. Write a
	// single decimal integer (kstrtoint).
	SysfsVHCIDetach = "detach"
	// SysfsVHCIStatus is the vhci status file for controller 0.
	SysfsVHCIStatus = "status"
	// SysfsVHCIStatusFmt is the printf format for non-primary controller
	// status files (controllers 1..nports/VHCI_HC_PORTS-1).
	SysfsVHCIStatusFmt = "status.%d"
	// SysfsVHCINPorts is the read-only nports attribute; total ports
	// across all controllers.
	SysfsVHCINPorts = "nports"

	// SysfsModuleDir is the base path under which loaded kernel modules
	// appear as subdirectories.
	SysfsModuleDir = "/sys/module"
)

// Kernel module names probed by ModulesAvailable. Verbatim from the
// sysfs entry names; dashes are replaced by underscores per kernel
// convention.
const (
	// ModuleUsbipCore is the shared usbip core module.
	ModuleUsbipCore = "usbip_core"
	// ModuleUsbipHost is the exporter-side usbip-host stub driver.
	ModuleUsbipHost = "usbip_host"
	// ModuleVHCIHCD is the importer-side vhci_hcd virtual host controller.
	ModuleVHCIHCD = "vhci_hcd"
)

// Write payloads whose exact byte sequence is dictated by the kernel
// parsers (see v1 contract §6.1 "Key facts verified from kernel source").
const (
	// MatchBusIDAddPrefix is the prefix for "add <busid>" writes to
	// match_busid. Includes trailing space.
	MatchBusIDAddPrefix = "add "
	// MatchBusIDDelPrefix is the prefix for "del <busid>" writes to
	// match_busid. Includes trailing space.
	MatchBusIDDelPrefix = "del "

	// UsbipSockfdDisconnect is the payload written to usbip_sockfd to
	// trigger SDEV_EVENT_DOWN (graceful disconnect).
	UsbipSockfdDisconnect = "-1"
)

// VHCIAttachFmt is the printf format used by vhci_sysfs::attach_store.
// Kernel source uses sscanf("%u %u %u %u") but upstream client writes
// a signed fd ("%u %d %u %u") for byte-for-byte interop.
const VHCIAttachFmt = "%u %d %u %u"

// VHCIStatusRowFmt is the scanf format used by the vhci_hcd.c status
// file. Verbatim from drivers/usb/usbip/vhci_sysfs.c.
const VHCIStatusRowFmt = "%s  %04u %03u %03u %08x %06u %s"

// VHCIStatusHeaderPrefix is the literal prefix of the status file's
// optional header line. The parser skips any line starting with this
// prefix regardless of trailing whitespace variation.
const VHCIStatusHeaderPrefix = "hub"

// Hub-type tokens used as the first whitespace-delimited token of each
// vhci status row. "hs" identifies a USB 2.x slot; "ss" identifies a
// USB 3.x slot.
const (
	// HubTypeHighSpeed marks a high-speed slot in the status file.
	HubTypeHighSpeed = "hs"
	// HubTypeSuperSpeed marks a SuperSpeed slot in the status file.
	HubTypeSuperSpeed = "ss"
)
