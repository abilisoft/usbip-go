package domain

const (
	// deviceIDBusShift is the bit offset of the busnum field inside a DeviceID.
	deviceIDBusShift = 16
	// deviceIDLowMask masks the low 16 bits (devnum).
	deviceIDLowMask = 0xFFFF
)

// DeviceID encodes (busnum << 16) | devnum, used in wire OP_REP_DEVLIST.
type DeviceID uint32

// BusNum returns the bus number encoded in the high 16 bits.
func (d DeviceID) BusNum() uint16 { return uint16((d >> deviceIDBusShift) & deviceIDLowMask) }

// DevNum returns the device number encoded in the low 16 bits.
func (d DeviceID) DevNum() uint16 { return uint16(d & deviceIDLowMask) }
