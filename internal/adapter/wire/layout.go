package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// DeviceWireSize is the on-wire width of the 312-byte device descriptor
// layout (spec §6.2). This is the OP_REP_DEVLIST and OP_REP_IMPORT
// device-body size.
const DeviceWireSize = 312

// Byte offsets into the device descriptor (spec §6.2).
const (
	offDevPath        = 0
	offDevBusID       = 256
	offDevBusNum      = 288
	offDevDevNum      = 292
	offDevSpeed       = 296
	offDevVendorID    = 300
	offDevProductID   = 302
	offDevBcdDevice   = 304
	offDevClass       = 306
	offDevSubclass    = 307
	offDevProtocol    = 308
	offDevConfigValue = 309
	offDevNumConfigs  = 310
	offDevNumIntfs    = 311
)

//gremlins:target
// ^ spec §8.6 / plan 2.3: this file is a primary mutation-testing
// target because every byte offset, endianness choice, and overflow
// guard affects upstream interop.

// errDeviceFieldTooLarge wraps domain.ErrProtocolError per spec §6.4
// error-matrix rules (protocol-level overflow is a protocol error,
// not a domain-level sentinel). Kept as a package-internal identity
// so callers in this package can disambiguate via errors.Is, and
// upstream callers still match on the public domain.ErrProtocolError.
// wire field holds a value that does not fit the corresponding domain
// u16. Upstream kernel never emits such values, but a hostile or
// corrupted peer could.
var errDeviceFieldTooLarge = fmt.Errorf("%w: device descriptor field exceeds u16 range", domain.ErrProtocolError)

// EncodeDevice serializes d into the 312-byte on-wire device descriptor
// format (spec §6.2) and writes it to w.
//
// Returns ErrBusIDInvalid if d.BusID >= 32 bytes, ErrProtocolError if
// d.Path >= 256 bytes, and propagates underlying writer errors wrapped.
func EncodeDevice(w io.Writer, d domain.Device) error {
	err := WritePaddedString(w, d.Path, domain.SysPathSize)
	if err != nil {
		return err
	}

	err = WritePaddedString(w, string(d.BusID), domain.BusIDSize)
	if err != nil {
		return err
	}

	tail := make([]byte, DeviceWireSize-deviceTailStart)
	encodeDeviceTail(tail, d)

	_, err = w.Write(tail)
	if err != nil {
		return fmt.Errorf("write device descriptor tail: %w", err)
	}

	return nil
}

// deviceTailStart is the offset of the first numeric field (busnum) in
// the device descriptor. Tail-relative offsets subtract this base.
const deviceTailStart = offDevBusNum

// encodeDeviceTail writes the 24-byte numeric-field portion of the
// device descriptor (offsets 288..311) into buf, which MUST be at least
// DeviceWireSize-deviceTailStart bytes long.
func encodeDeviceTail(buf []byte, d domain.Device) {
	binary.BigEndian.PutUint32(buf[offDevBusNum-deviceTailStart:], uint32(d.BusNum))
	binary.BigEndian.PutUint32(buf[offDevDevNum-deviceTailStart:], uint32(d.DevNum))
	binary.BigEndian.PutUint32(buf[offDevSpeed-deviceTailStart:], uint32(d.Speed))
	binary.BigEndian.PutUint16(buf[offDevVendorID-deviceTailStart:], d.VendorID)
	binary.BigEndian.PutUint16(buf[offDevProductID-deviceTailStart:], d.ProductID)
	binary.BigEndian.PutUint16(buf[offDevBcdDevice-deviceTailStart:], d.BcdDevice)

	buf[offDevClass-deviceTailStart] = uint8(d.Class)
	buf[offDevSubclass-deviceTailStart] = uint8(d.Subclass)
	buf[offDevProtocol-deviceTailStart] = uint8(d.Protocol)
	buf[offDevConfigValue-deviceTailStart] = d.ConfigValue
	buf[offDevNumConfigs-deviceTailStart] = d.NumConfigs
	buf[offDevNumIntfs-deviceTailStart] = d.NumInterfaces
}

// DecodeDevice reads 312 bytes from r and returns the decoded Device.
// Short reads are surfaced as io.ErrUnexpectedEOF wrapped with field
// context. Oversized busnum/devnum u32 fields (> uint16 max) are
// rejected with ErrProtocolError.
func DecodeDevice(r io.Reader) (domain.Device, error) {
	buf := make([]byte, DeviceWireSize)

	_, err := io.ReadFull(r, buf)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return domain.Device{}, fmt.Errorf("read device descriptor: %w", io.ErrUnexpectedEOF)
		}

		return domain.Device{}, fmt.Errorf("read device descriptor: %w", err)
	}

	return decodeDeviceBuf(buf)
}

// decodeDeviceBuf interprets a 312-byte slice as a device descriptor.
// The truncated flags from paddedStringFromBytes are intentionally
// dropped: per-field NUL-termination drift is advisory and not
// surfaced at the Device level. Higher-level Codec methods (which
// have an injected *slog.Logger) are responsible for any logging.
func decodeDeviceBuf(buf []byte) (domain.Device, error) {
	path, _ := paddedStringFromBytes(buf[offDevPath:offDevBusID])
	busidStr, _ := paddedStringFromBytes(buf[offDevBusID:offDevBusNum])

	busnum32 := binary.BigEndian.Uint32(buf[offDevBusNum:])
	devnum32 := binary.BigEndian.Uint32(buf[offDevDevNum:])
	speed := binary.BigEndian.Uint32(buf[offDevSpeed:])

	if busnum32 > uint32(^uint16(0)) {
		return domain.Device{}, fmt.Errorf("%w: busnum=%d", errDeviceFieldTooLarge, busnum32)
	}

	if devnum32 > uint32(^uint16(0)) {
		return domain.Device{}, fmt.Errorf("%w: devnum=%d", errDeviceFieldTooLarge, devnum32)
	}

	return domain.Device{
		Path:          path,
		BusID:         domain.BusID(busidStr),
		BusNum:        uint16(busnum32),
		DevNum:        uint16(devnum32),
		Speed:         domain.Speed(speed),
		VendorID:      binary.BigEndian.Uint16(buf[offDevVendorID:]),
		ProductID:     binary.BigEndian.Uint16(buf[offDevProductID:]),
		BcdDevice:     binary.BigEndian.Uint16(buf[offDevBcdDevice:]),
		Class:         domain.USBClass(buf[offDevClass]),
		Subclass:      domain.USBSubclass(buf[offDevSubclass]),
		Protocol:      domain.USBProtocol(buf[offDevProtocol]),
		ConfigValue:   buf[offDevConfigValue],
		NumConfigs:    buf[offDevNumConfigs],
		NumInterfaces: buf[offDevNumIntfs],
	}, nil
}
