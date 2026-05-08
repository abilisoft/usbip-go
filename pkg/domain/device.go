// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

// Device represents a USB device, either local (discovered via sysfs) or
// remote (decoded from an OP_REP_DEVLIST response). Fields follow Go
// naming conventions (noun-adjective) rather than USB spec names where
// the spec uses C-style prefixes.
type Device struct {
	// Path is the sysfs path, meaningful for local devices only. It is
	// the empty string for Devices decoded from a remote OP_REP_DEVLIST.
	Path string

	BusID  BusID
	BusNum uint16
	DevNum uint16
	Speed  Speed

	// VendorID is the USB spec idVendor.
	VendorID uint16
	// ProductID is the USB spec idProduct.
	ProductID uint16
	// BcdDevice is the USB spec bcdDevice release number. The USB-spec
	// name is retained because it refers to a specific BCD-encoded field
	// on the wire and is not a mere prefix convention.
	BcdDevice uint16

	Class         USBClass
	Subclass      USBSubclass
	Protocol      USBProtocol
	ConfigValue   uint8
	NumConfigs    uint8
	NumInterfaces uint8

	// Manufacturer is the device-supplied iManufacturer string descriptor
	// resolved from the kernel via /sys/bus/usb/devices/<busid>/manufacturer.
	// Empty when the device descriptor's iManufacturer index is 0 or the
	// device does not implement string descriptors. Meaningful for local
	// devices only — OP_REP_DEVLIST does not carry these strings on the wire.
	Manufacturer string

	// Product is the device-supplied iProduct string descriptor (the human
	// product name, e.g. "AX88179 Gigabit Ethernet"). Distinct from
	// ProductID which is the numeric idProduct. Empty when not populated.
	// Meaningful for local devices only.
	Product string

	// Interfaces is an owned, read-only slice. Consumers MUST NOT mutate
	// the returned slice; future versions may copy-on-return if mutation
	// causes bugs.
	Interfaces []Interface
}

// Interface describes a USB interface (bInterfaceClass/bInterfaceSubClass
// /bInterfaceProtocol triple).
//
// Consumers MUST NOT mutate Device.Interfaces; treat it as read-only.
type Interface struct {
	Class    USBClass
	Subclass USBSubclass
	Protocol USBProtocol

	// Alt is the bAlternateSetting. It is meaningful ONLY for Devices
	// constructed from local sysfs (e.g. ExporterKernel.ListLocalDevices).
	//
	// OP_REP_DEVLIST encodes each interface as 4 bytes — class, subclass,
	// protocol, padding — with no bAlternateSetting on the wire. For
	// remotely-listed Devices, Alt is always zero and MUST NOT be
	// interpreted as meaningful.
	Alt uint8
}
