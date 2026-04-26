// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import "strconv"

// Speed is a USB speed enum matching kernel enum usb_device_speed values.
// Sysfs reports Mbps strings ("5000"), not these integers; the kernel
// adapter translates via ReadSpeedAttr before populating this field.
type Speed uint32

// USB device speeds, numeric values match the kernel's usb_device_speed.
const (
	SpeedUnknown   Speed = 0
	SpeedLow       Speed = 1 // 1.5 Mbit/s
	SpeedFull      Speed = 2 // 12 Mbit/s
	SpeedHigh      Speed = 3 // 480 Mbit/s
	SpeedWireless  Speed = 4
	SpeedSuper     Speed = 5 // 5 Gbit/s
	SpeedSuperPlus Speed = 6 // 10 Gbit/s
)

// IsKnown reports whether s falls inside the finite enum declared
// above. Wire decoders call IsKnown after reading the 4-byte field
// so a peer emitting a value the kernel does not define cannot
// round-trip as a mystery domain.Speed that downstream consumers
// (metrics, CLI rendering, event delivery) would silently carry.
func (s Speed) IsKnown() bool {
	switch s {
	case SpeedUnknown, SpeedLow, SpeedFull, SpeedHigh,
		SpeedWireless, SpeedSuper, SpeedSuperPlus:
		return true
	default:
		return false
	}
}

// String returns a human-readable description of the speed.
// Unknown values return "speed(N)" with N in decimal.
func (s Speed) String() string {
	switch s {
	case SpeedUnknown:
		return "unknown"
	case SpeedLow:
		return "Low-Speed (1.5Mbps)"
	case SpeedFull:
		return "Full-Speed (12Mbps)"
	case SpeedHigh:
		return "High-Speed (480Mbps)"
	case SpeedWireless:
		return "Wireless"
	case SpeedSuper:
		return "SuperSpeed (5Gbps)"
	case SpeedSuperPlus:
		return "SuperSpeed+ (10Gbps)"
	default:
		return "speed(" + strconv.FormatUint(uint64(s), 10) + ")"
	}
}
