package wire

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"

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

// ReadPaddedString reads exactly size bytes from r and returns the
// string terminated at the first NUL, or the full buffer if no NUL is
// present (with a Warn logged to slog.Default).
//
// On short read returns io.ErrUnexpectedEOF wrapped with size context.
func ReadPaddedString(r io.Reader, size int) (string, error) {
	buf := make([]byte, size)

	_, err := io.ReadFull(r, buf)
	if err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			return "", fmt.Errorf("read padded string (size=%d): %w", size, io.ErrUnexpectedEOF)
		}

		return "", fmt.Errorf("read padded string (size=%d): %w", size, err)
	}

	return paddedStringFromBytes(buf), nil
}

// paddedStringFromBytes interprets buf as a NUL-padded fixed-width
// string. If no NUL is present the entire buffer is returned and a
// Warn is emitted to slog.Default. Used for decoding already-read
// fixed-width payloads (e.g. device descriptor path / busid slices).
func paddedStringFromBytes(buf []byte) string {
	before, _, found := bytes.Cut(buf, []byte{0})
	if !found {
		slog.Warn("non-NUL-terminated padded string", "size", len(buf))

		return string(buf)
	}

	return string(before)
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
