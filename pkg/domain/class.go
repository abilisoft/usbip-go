// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

// USBClass is a USB device/interface class code (bDeviceClass/bInterfaceClass).
type USBClass uint8

// USBSubclass is a USB device/interface subclass code. Subclass semantics
// depend on the owning USBClass; see usb.org class specifications.
type USBSubclass uint8

// USBProtocol is a USB device/interface protocol code. Protocol semantics
// depend on the owning USBClass + USBSubclass.
type USBProtocol uint8

// String returns the canonical name from the committed usb.ids subset
// (see class_data.go). Unknown values return "class(0xNN)".
func (c USBClass) String() string {
	if name, ok := usbClassName(c); ok {
		return name
	}

	return "class(0x" + hexByte(uint8(c)) + ")"
}

// String returns a hex-formatted subclass code (Subclass meaning is only
// defined within its owning class; the committed data is class-indexed).
func (s USBSubclass) String() string {
	return "subclass(0x" + hexByte(uint8(s)) + ")"
}

// String returns a hex-formatted protocol code (Protocol meaning is only
// defined within its owning class+subclass).
func (p USBProtocol) String() string {
	return "protocol(0x" + hexByte(uint8(p)) + ")"
}

// hexByte formats a byte as two lowercase hex digits.
func hexByte(b uint8) string {
	const hex = "0123456789abcdef"

	return string([]byte{hex[b>>4], hex[b&0xF]})
}
