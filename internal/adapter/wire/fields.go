// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// DecodeFlags carries advisory signals produced by a decode call that
// the v1 contract §6.2 permissive-read rule keeps out of the error channel.
// Codec methods consume the flags and emit slog.Warn records; direct
// callers of the package-level decoders can inspect the struct or
// ignore it. An empty DecodeFlags represents a clean decode.
type DecodeFlags struct {
	// TruncatedPaddedStrings records every fixed-width string field
	// whose bytes reached the end of the field without a NUL
	// terminator. Each entry names the field and the 0-based device
	// position inside the reply (0 for both single-device replies and
	// the first device of a devlist).
	TruncatedPaddedStrings []PaddedStringTruncation
	// TrailingBytes is true when the decoder observed bytes after the
	// declared frame boundary. Currently set only by
	// DecodeOpRepDevlist.
	TrailingBytes bool
}

// PaddedStringTruncation identifies one truncated padded-string
// field in a decoded payload.
type PaddedStringTruncation struct {
	// Field is a dotted identifier naming the truncated field
	// (e.g. "device.path", "device.busid").
	Field string
	// DeviceIndex is the 0-based position inside the reply. Devlist
	// decoders overwrite it with the in-slice index of each device;
	// single-device decoders (DecodeOpRepImport, direct DecodeDevice)
	// leave it at 0.
	DeviceIndex int
}

// WritePaddedString writes s NUL-padded to exactly size bytes into w.
//
// The USBIP wire format encodes path and busid fields as fixed-width
// byte arrays, where a trailing NUL indicates end-of-string. Strings
// must therefore be strictly shorter than size (len(s) <= size-1) to
// leave room for at least one NUL terminator. On overflow the returned
// error depends on the field:
//
//   - size == BusIDSize → ErrBusIDInvalid (busid is the public
//     programmer-visible field; misuse should match the public sentinel)
//   - otherwise         → ErrProtocolError (path and other fixed-width
//     fields are protocol-level; misuse is an internal protocol error)
func WritePaddedString(w io.Writer, s string, size int) error {
	if len(s) >= size {
		return overflowErr(len(s), size)
	}

	buf := make([]byte, size)
	copy(buf, s)

	_, err := w.Write(buf)
	if err != nil {
		return fmt.Errorf("write padded string: %w", err)
	}

	return nil
}

// ReadPaddedString reads exactly size bytes from r, truncates at the
// first NUL byte, and returns the decoded string together with a flag
// indicating whether the buffer was non-NUL-terminated (i.e. used the
// full size because no NUL was found).
//
// The caller decides what to do with truncated == true; this helper
// does not log. Non-terminated frames are permissive on read (spec
// §6.2: "Non-NUL-terminated padded string → truncated at first
// non-printable or end of buffer; no error; logged as slog.Warn" —
// the logging is done by the Codec method, which has an injected
// *slog.Logger; see Codec.DecodeOpRepDevlist and related methods).
//
// On short read returns io.ErrUnexpectedEOF wrapped with size context.
func ReadPaddedString(r io.Reader, size int) (string, bool, error) {
	buf := make([]byte, size)

	_, err := io.ReadFull(r, buf)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return "", false, fmt.Errorf("read padded string (size=%d): %w", size, io.ErrUnexpectedEOF)
		}

		return "", false, fmt.Errorf("read padded string (size=%d): %w", size, err)
	}

	s, truncated := paddedStringFromBytes(buf)

	return s, truncated, nil
}

// minPrintableASCII and maxPrintableASCII bound the printable subset
// of ASCII (0x20 space through 0x7E tilde). Bytes outside this range —
// NUL, control characters, DEL, and any high-bit byte — are not valid
// in USBIP padded-string fields per v1 contract §6.2.
const (
	minPrintableASCII = 0x20
	maxPrintableASCII = 0x7E
)

// paddedStringFromBytes interprets buf as a NUL-padded fixed-width
// string. Returns the decoded string and a truncated flag, matching
// v1 contract §6.2 semantics:
//
//   - First byte is NUL-or-non-printable at offset i > 0: return
//     buf[:i] and truncated == false (the well-formed case: NUL
//     terminator found, or garbage after valid ASCII). No warn.
//   - Well-formed NUL terminator (NUL within the buffer): truncated
//     == false regardless.
//   - Buffer is entirely printable with no NUL: return string(buf)
//     and truncated == true (the malformed case: padding is missing).
//   - Non-printable-before-NUL encountered after printable prefix:
//     return the printable prefix and truncated == true.
//
// The spec language is "truncated at first non-printable or end of
// buffer". NUL is one non-printable byte; a zero-length prefix (first
// byte non-printable) is allowed and returns "".
func paddedStringFromBytes(buf []byte) (string, bool) {
	// Fast path for the typical well-formed case: locate the NUL and
	// return everything before it. If a non-printable byte appears
	// before the NUL we fall through to the slow scan.
	before, _, found := bytes.Cut(buf, []byte{0})
	if found && isAllPrintable(before) {
		return string(before), false
	}

	// Slow path: scan byte-by-byte, stop at first non-printable.
	for i, b := range buf {
		if b < minPrintableASCII || b > maxPrintableASCII {
			// Truncated flag distinguishes the clean NUL-before-non-
			// printable case (handled above) from everything else.
			// Reaching here means either no NUL at all, or NUL is
			// preceded by a non-printable byte — both malformed.
			return string(buf[:i]), true
		}
	}

	// All bytes printable, no NUL: spec's end-of-buffer case.
	return string(buf), true
}

// isAllPrintable reports whether every byte in buf lies in the
// printable ASCII range (0x20..0x7E).
func isAllPrintable(buf []byte) bool {
	for _, b := range buf {
		if b < minPrintableASCII || b > maxPrintableASCII {
			return false
		}
	}

	return true
}

// overflowErr maps a padded-string overflow to the spec's error matrix:
// busid field → ErrBusIDInvalid, other fields → ErrProtocolError.
func overflowErr(length, size int) error {
	if size == domain.BusIDSize {
		return fmt.Errorf("%w: length %d exceeds max %d", domain.ErrBusIDInvalid, length, size-1)
	}

	return fmt.Errorf("%w: padded string length %d exceeds max %d",
		domain.ErrProtocolError, length, size-1)
}
