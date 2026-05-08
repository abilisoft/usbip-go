// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// DeviceWireSize is the on-wire width of the 312-byte device descriptor
// layout (v1 contract §6.2). This is the OP_REP_DEVLIST and OP_REP_IMPORT
// device-body size.
const DeviceWireSize = 312

// Byte offsets into the device descriptor (v1 contract §6.2).
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
// ^ v1 contract §8.6: this file is a primary mutation-testing
// target because every byte offset, endianness choice, and overflow
// guard affects upstream interop.

// errDeviceFieldTooLarge wraps domain.ErrProtocolError per v1 contract §6.4
// error-matrix rules (protocol-level overflow is a protocol error,
// not a domain-level sentinel). Kept as a package-internal identity
// so callers in this package can disambiguate via errors.Is, and
// upstream callers still match on the public domain.ErrProtocolError.
// wire field holds a value that does not fit the corresponding domain
// u16. Upstream kernel never emits such values, but a hostile or
// corrupted peer could.
var errDeviceFieldTooLarge = fmt.Errorf("%w: device descriptor field exceeds u16 range", domain.ErrProtocolError)

// EncodeDevice serializes d into the 312-byte on-wire device descriptor
// format (v1 contract §6.2) and writes it to w.
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

// DecodeDevice reads 312 bytes from r and returns the decoded Device
// plus the advisory DecodeFlags that record any padded-string
// truncation the §6.2 permissive-read rule keeps out of the error
// channel. Short reads surface as io.ErrUnexpectedEOF wrapped with
// field context. Oversized busnum/devnum u32 fields (> uint16 max)
// and unknown Speed values surface as ErrProtocolError.
func DecodeDevice(r io.Reader) (domain.Device, DecodeFlags, error) {
	buf := make([]byte, DeviceWireSize)

	_, err := io.ReadFull(r, buf)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return domain.Device{}, DecodeFlags{},
				fmt.Errorf("read device descriptor: %w", io.ErrUnexpectedEOF)
		}

		return domain.Device{}, DecodeFlags{},
			fmt.Errorf("read device descriptor: %w", err)
	}

	return decodeDeviceBuf(buf)
}

// decodeDeviceBuf interprets a 312-byte slice as a device descriptor
// and returns (Device, DecodeFlags, error). The flags capture every
// padded-string field whose bytes reached the end without NUL; the
// §6.2 rule keeps that condition non-erroring, so the Codec layer
// reads the flags and emits slog.Warn records. DeviceIndex defaults
// to 0; devlist callers overwrite it with the in-slice position.
func decodeDeviceBuf(buf []byte) (domain.Device, DecodeFlags, error) {
	path, pathTruncated := paddedStringFromBytes(buf[offDevPath:offDevBusID])
	busidStr, busidTruncated := paddedStringFromBytes(buf[offDevBusID:offDevBusNum])

	busnum32 := binary.BigEndian.Uint32(buf[offDevBusNum:])
	devnum32 := binary.BigEndian.Uint32(buf[offDevDevNum:])
	speed := binary.BigEndian.Uint32(buf[offDevSpeed:])

	if busnum32 > uint32(^uint16(0)) {
		return domain.Device{}, DecodeFlags{},
			fmt.Errorf("%w: busnum=%d", errDeviceFieldTooLarge, busnum32)
	}

	if devnum32 > uint32(^uint16(0)) {
		return domain.Device{}, DecodeFlags{},
			fmt.Errorf("%w: devnum=%d", errDeviceFieldTooLarge, devnum32)
	}

	speedValue := domain.Speed(speed)
	if !speedValue.IsKnown() {
		return domain.Device{}, DecodeFlags{},
			fmt.Errorf("%w: speed %d not in kernel enum_device_speed",
				domain.ErrProtocolError, speed)
	}

	var flags DecodeFlags

	if pathTruncated {
		flags.TruncatedPaddedStrings = append(flags.TruncatedPaddedStrings,
			PaddedStringTruncation{Field: "device.path"})
	}

	if busidTruncated {
		flags.TruncatedPaddedStrings = append(flags.TruncatedPaddedStrings,
			PaddedStringTruncation{Field: "device.busid"})
	}

	return domain.Device{
		Path:          path,
		BusID:         domain.BusID(busidStr),
		BusNum:        uint16(busnum32),
		DevNum:        uint16(devnum32),
		Speed:         speedValue,
		VendorID:      binary.BigEndian.Uint16(buf[offDevVendorID:]),
		ProductID:     binary.BigEndian.Uint16(buf[offDevProductID:]),
		BcdDevice:     binary.BigEndian.Uint16(buf[offDevBcdDevice:]),
		Class:         domain.USBClass(buf[offDevClass]),
		Subclass:      domain.USBSubclass(buf[offDevSubclass]),
		Protocol:      domain.USBProtocol(buf[offDevProtocol]),
		ConfigValue:   buf[offDevConfigValue],
		NumConfigs:    buf[offDevNumConfigs],
		NumInterfaces: buf[offDevNumIntfs],
	}, flags, nil
}
