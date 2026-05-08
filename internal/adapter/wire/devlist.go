// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// interfaceWireSize is the on-wire width of a single interface descriptor
// in OP_REP_DEVLIST (v1 contract §6.2: class u8, subclass u8, protocol u8,
// padding u8).
const interfaceWireSize = 4

// devlistCountSize is the u32 field that follows the OP header in a
// OP_REP_DEVLIST reply.
const devlistCountSize = 4

// MaxDevlistDevices caps the device count honoured by DecodeOpRepDevlist.
// A peer that declares more devices than this has either a pathological
// enumeration or is mounting a denial-of-service attack against the
// importer's allocator. 1024 is already well past the plausible ceiling
// for any real USB hub topology.
const MaxDevlistDevices = 1024

// Byte offsets inside a single interface descriptor.
const (
	offIntfClass    = 0
	offIntfSubclass = 1
	offIntfProtocol = 2
	// offIntfPadding = 3 — reserved, zero on encode, ignored on decode.
)

// errDevlistTruncated is an internal sentinel distinguishing the two
// error-matrix rows (truncated mid-device vs truncated interfaces). The
// wrapping message text is the public contract (v1 contract §6.2).
var errDevlistTruncated = errors.New("devlist truncated")

// errDevlistCountOverflow surfaces an oversized device-count on encode.
// The u32 on-wire field holds the device count; a caller supplying more
// than math.MaxUint32 devices is programmer error.
var errDevlistCountOverflow = errors.New("devlist device count overflows u32")

// deviceCountU32 narrows an int to u32 with a bounds check, avoiding
// the silent truncation that G115 rejects.
func deviceCountU32(n int) (uint32, error) {
	if n < 0 || uint64(n) > uint64(math.MaxUint32) {
		return 0, fmt.Errorf("%w: %d", errDevlistCountOverflow, n)
	}

	return uint32(n), nil
}

// EncodeOpReqDevlist returns the 8-byte OP_REQ_DEVLIST request (pure
// header). The request carries no body (v1 contract §6.2).
func EncodeOpReqDevlist() []byte {
	return EncodeHeader(OpReqDevlist, 0)
}

// EncodeOpRepDevlist writes an OP_REP_DEVLIST reply for the supplied
// devices (v1 contract §6.2). Each device is serialized via EncodeDevice
// followed by its NumInterfaces four-byte interface descriptors.
//
// When d.NumInterfaces does not match len(d.Interfaces) the declared
// count wins (matching upstream usbipd behavior): extra interfaces in
// the slice are dropped; missing entries are padded with zero bytes.
func EncodeOpRepDevlist(w io.Writer, devices []domain.Device) error {
	header := EncodeHeader(OpRepDevlist, 0)

	_, err := w.Write(header)
	if err != nil {
		return fmt.Errorf("write OP_REP_DEVLIST header: %w", err)
	}

	count, err := deviceCountU32(len(devices))
	if err != nil {
		return err
	}

	countBuf := make([]byte, devlistCountSize)
	binary.BigEndian.PutUint32(countBuf, count)

	_, err = w.Write(countBuf)
	if err != nil {
		return fmt.Errorf("write OP_REP_DEVLIST count: %w", err)
	}

	for i, d := range devices {
		err = EncodeDevice(w, d)
		if err != nil {
			return fmt.Errorf("encode device %d: %w", i, err)
		}

		err = encodeInterfaces(w, d)
		if err != nil {
			return err
		}
	}

	return nil
}

// encodeInterfaces writes d.NumInterfaces interface descriptors.
// If len(d.Interfaces) < d.NumInterfaces, missing entries are zeroed.
// If len(d.Interfaces) > d.NumInterfaces, trailing entries are dropped.
func encodeInterfaces(w io.Writer, d domain.Device) error {
	total := int(d.NumInterfaces)

	buf := make([]byte, interfaceWireSize)
	for i := range total {
		clear(buf)

		if i < len(d.Interfaces) {
			buf[offIntfClass] = uint8(d.Interfaces[i].Class)
			buf[offIntfSubclass] = uint8(d.Interfaces[i].Subclass)
			buf[offIntfProtocol] = uint8(d.Interfaces[i].Protocol)
		}

		_, err := w.Write(buf)
		if err != nil {
			return fmt.Errorf("write interface %d: %w", i, err)
		}
	}

	return nil
}

// DecodeOpRepDevlist decodes an OP_REP_DEVLIST reply from r. Returns
// (nil, false, nil) for a zero-device reply. The trailingBytes flag
// is true when bytes remain after the last device (v1 contract §6.2
// "permissive on read"); the caller (typically the Codec) logs via
// its injected logger if desired. Errors follow v1 contract §6.2 error
// matrix:
//
//   - Truncated mid-device → io.ErrUnexpectedEOF wrapped with
//     "truncated devlist at index N" where N is the count of devices
//     successfully decoded so far.
//   - Truncated mid-interface / declared interface count exceeds
//     remaining bytes → io.ErrUnexpectedEOF wrapped with
//     "truncated interfaces".
func DecodeOpRepDevlist(r io.Reader) ([]domain.Device, DecodeFlags, error) {
	_, op, _, err := DecodeHeader(r)
	if err != nil {
		return nil, DecodeFlags{}, err
	}

	if op != OpRepDevlist {
		return nil, DecodeFlags{}, fmt.Errorf("%w: expected OP_REP_DEVLIST got 0x%04x",
			domain.ErrProtocolMismatch, uint16(op))
	}

	countBuf := make([]byte, devlistCountSize)

	_, err = io.ReadFull(r, countBuf)
	if err != nil {
		return nil, DecodeFlags{}, wrapUnexpectedEOF("read devlist count", err)
	}

	count := binary.BigEndian.Uint32(countBuf)

	// Cap the declared device count BEFORE allocating the destination
	// slice. A hostile peer advertising near-MaxUint32 would otherwise
	// trigger a makeslice panic (or in the best case a multi-GB
	// allocation).
	if count > MaxDevlistDevices {
		return nil, DecodeFlags{}, fmt.Errorf("%w: devlist count %d exceeds cap %d",
			domain.ErrProtocolMismatch, count, MaxDevlistDevices)
	}

	// Use a bufio.Reader so we can Peek to detect trailing bytes.
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}

	devices, flags, err := decodeDevlistBody(br, count)
	if err != nil {
		return nil, DecodeFlags{}, err
	}

	flags.TrailingBytes = hasTrailingBytes(br)

	return devices, flags, nil
}

// decodeDevlistBody reads exactly count devices, each followed by its
// interface descriptors. Per-device truncation flags are merged into
// the returned DecodeFlags with DeviceIndex set to the in-slice
// position of each device.
func decodeDevlistBody(r io.Reader, count uint32) ([]domain.Device, DecodeFlags, error) {
	var flags DecodeFlags

	if count == 0 {
		return nil, flags, nil
	}

	devices := make([]domain.Device, 0, count)

	for i := range count {
		dev, devFlags, err := decodeDevlistDeviceAt(r, i)
		if err != nil {
			return nil, DecodeFlags{}, err
		}

		mergeDeviceTruncationFlags(&flags, devFlags, int(i))

		devices = append(devices, dev)
	}

	return devices, flags, nil
}

// decodeDevlistDeviceAt decodes one device + its interface list,
// classifying a truncated decode as errDevlistTruncated-wrapped so
// the caller can distinguish it from a well-formed device whose own
// padded-string fields simply were not NUL-terminated.
func decodeDevlistDeviceAt(r io.Reader, idx uint32) (domain.Device, DecodeFlags, error) {
	dev, devFlags, err := DecodeDevice(r)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return domain.Device{}, DecodeFlags{},
				fmt.Errorf("%w: truncated devlist at index %d: %w",
					errDevlistTruncated, idx, io.ErrUnexpectedEOF)
		}

		return domain.Device{}, DecodeFlags{},
			fmt.Errorf("decode device at index %d: %w", idx, err)
	}

	intfs, err := decodeInterfaces(r, dev.NumInterfaces)
	if err != nil {
		return domain.Device{}, DecodeFlags{}, err
	}

	dev.Interfaces = intfs

	return dev, devFlags, nil
}

// mergeDeviceTruncationFlags copies every truncation entry from src
// into dst with DeviceIndex rewritten to idx. Split out to keep
// decodeDevlistBody under the project's cognitive-complexity cap.
func mergeDeviceTruncationFlags(dst *DecodeFlags, src DecodeFlags, idx int) {
	for _, trunc := range src.TruncatedPaddedStrings {
		trunc.DeviceIndex = idx
		dst.TruncatedPaddedStrings = append(dst.TruncatedPaddedStrings, trunc)
	}
}

// decodeInterfaces reads count interface descriptors from r.
func decodeInterfaces(r io.Reader, count uint8) ([]domain.Interface, error) {
	if count == 0 {
		return nil, nil
	}

	buf := make([]byte, int(count)*interfaceWireSize)

	_, err := io.ReadFull(r, buf)
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, fmt.Errorf("%w: truncated interfaces: %w",
				errDevlistTruncated, io.ErrUnexpectedEOF)
		}

		return nil, fmt.Errorf("read interfaces: %w", err)
	}

	intfs := make([]domain.Interface, 0, count)

	for i := range int(count) {
		base := i * interfaceWireSize

		intfs = append(intfs, domain.Interface{
			Class:    domain.USBClass(buf[base+offIntfClass]),
			Subclass: domain.USBSubclass(buf[base+offIntfSubclass]),
			Protocol: domain.USBProtocol(buf[base+offIntfProtocol]),
			// Alt is not on the wire — v1 contract §6.2 mandates 0 on decode.
			Alt: 0,
		})
	}

	return intfs, nil
}

// wrapUnexpectedEOF maps io.EOF / io.ErrUnexpectedEOF to
// io.ErrUnexpectedEOF wrapped with ctx; other errors pass through
// wrapped verbatim.
func wrapUnexpectedEOF(ctx string, err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%s: %w", ctx, io.ErrUnexpectedEOF)
	}

	return fmt.Errorf("%s: %w", ctx, err)
}

// hasTrailingBytes reports whether br still has at least one byte
// buffered. Callers (typically the Codec) use this to surface a
// permissive-read signal to an injected logger (v1 contract §6.2).
func hasTrailingBytes(br *bufio.Reader) bool {
	_, err := br.Peek(1)

	return err == nil
}
