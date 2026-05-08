package wire

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// interfaceWireSize is the on-wire width of a single interface descriptor
// in OP_REP_DEVLIST (spec §6.2: class u8, subclass u8, protocol u8,
// padding u8).
const interfaceWireSize = 4

// devlistCountSize is the u32 field that follows the OP header in a
// OP_REP_DEVLIST reply.
const devlistCountSize = 4

// Byte offsets inside a single interface descriptor.
const (
	offIntfClass    = 0
	offIntfSubclass = 1
	offIntfProtocol = 2
	// offIntfPadding = 3 — reserved, zero on encode, ignored on decode.
)

// errDevlistTruncated is an internal sentinel distinguishing the two
// error-matrix rows (truncated mid-device vs truncated interfaces). The
// wrapping message text is the public contract (spec §6.2).
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
// header). The request carries no body (spec §6.2).
func EncodeOpReqDevlist() []byte {
	return EncodeHeader(OpReqDevlist, 0)
}

// EncodeOpRepDevlist writes an OP_REP_DEVLIST reply for the supplied
// devices (spec §6.2). Each device is serialized via EncodeDevice
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
// (nil, nil) for a zero-device reply. Errors follow spec §6.2 error
// matrix:
//
//   - Truncated mid-device → io.ErrUnexpectedEOF wrapped with
//     "truncated devlist at index N" where N is the count of devices
//     successfully decoded so far.
//   - Truncated mid-interface / declared interface count exceeds
//     remaining bytes → io.ErrUnexpectedEOF wrapped with
//     "truncated interfaces".
//   - Trailing bytes → slog.Warn; accepted.
func DecodeOpRepDevlist(r io.Reader) ([]domain.Device, error) {
	_, op, _, err := DecodeHeader(r)
	if err != nil {
		return nil, err
	}

	if op != OpRepDevlist {
		return nil, fmt.Errorf("%w: expected OP_REP_DEVLIST got 0x%04x",
			domain.ErrProtocolMismatch, uint16(op))
	}

	countBuf := make([]byte, devlistCountSize)

	_, err = io.ReadFull(r, countBuf)
	if err != nil {
		return nil, wrapUnexpectedEOF("read devlist count", err)
	}

	count := binary.BigEndian.Uint32(countBuf)

	// Use a bufio.Reader so we can Peek to detect trailing bytes.
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}

	devices, err := decodeDevlistBody(br, count)
	if err != nil {
		return nil, err
	}

	warnIfTrailing(br)

	return devices, nil
}

// decodeDevlistBody reads exactly count devices, each followed by its
// interface descriptors.
func decodeDevlistBody(r io.Reader, count uint32) ([]domain.Device, error) {
	if count == 0 {
		return nil, nil
	}

	devices := make([]domain.Device, 0, count)

	for i := range count {
		dev, err := DecodeDevice(r)
		if err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, fmt.Errorf("%w: truncated devlist at index %d: %w",
					errDevlistTruncated, i, io.ErrUnexpectedEOF)
			}

			return nil, fmt.Errorf("decode device at index %d: %w", i, err)
		}

		intfs, err := decodeInterfaces(r, dev.NumInterfaces)
		if err != nil {
			return nil, err
		}

		dev.Interfaces = intfs
		devices = append(devices, dev)
	}

	return devices, nil
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
			// Alt is not on the wire — spec §6.2 mandates 0 on decode.
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

// warnIfTrailing emits a single slog.Warn if br has at least one more
// byte buffered. This is per spec §6.2 ("permissive on read").
func warnIfTrailing(br *bufio.Reader) {
	_, err := br.Peek(1)
	if err != nil {
		return
	}

	slog.Warn("trailing bytes after devlist")
}

