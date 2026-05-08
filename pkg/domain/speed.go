package domain

import "strconv"

// Speed is a USB speed enum matching kernel enum usb_device_speed values.
// Numeric values are the kernel values; no translation occurs on sysfs reads.
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
