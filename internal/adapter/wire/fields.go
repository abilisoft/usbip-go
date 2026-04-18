package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

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

// paddedStringFromBytes interprets buf as a NUL-padded fixed-width
// string. Returns the decoded string and a flag indicating whether no
// NUL was found (truncated == true; the full buffer contents are
// returned verbatim). Used for decoding already-read fixed-width
// payloads (e.g. device descriptor path / busid slices from a larger
// DecodeDevice buffer).
func paddedStringFromBytes(buf []byte) (string, bool) {
	before, _, found := bytes.Cut(buf, []byte{0})
	if !found {
		return string(buf), true
	}

	return string(before), false
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
