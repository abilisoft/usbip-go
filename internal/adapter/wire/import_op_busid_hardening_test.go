package wire_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestDecodeOpReqImportRejectsTruncated pins the invariant: when the
// wire busid field contains printable bytes followed by a NUL or
// non-printable (ReadPaddedString's truncation signal), the decoder
// must treat the input as malformed rather than silently accepting
// the printable prefix. A peer padding "1-1" with subsequent junk
// bytes otherwise lands a valid-looking busid on every downstream
// sysfs helper.
func TestDecodeOpReqImportRejectsTruncated(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpReqImport, 0))

	payload := make([]byte, domain.BusIDSize)
	// "1-1" followed by a non-printable byte (0x01). ReadPaddedString
	// returns the printable prefix with truncated=true; DecodeOpReqImport
	// must refuse rather than hand "1-1" to the sysfs path.
	copy(payload, []byte{'1', '-', '1', 0x01, 'x', 'x'})
	buf.Write(payload)

	_, err := wire.DecodeOpReqImport(&buf)
	require.Error(t, err,
		"decoder must refuse a truncated wire busid rather than accept the printable prefix")
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
}

// TestDecodeOpReqImportRejectsPathSeparator pins the charset boundary
// for peer-supplied busids: the wire-level validator must reject any
// byte that could escape a sysfs basename (the canonical exploit is
// '/' which lets a peer-crafted busid travel into path.Join and reach
// sibling devices' sysfs entries). The allowed set is ASCII letters,
// digits, '.', '_', '-' — sufficient for every real-world busid
// shape including the vudc name "usbip-vudc.0".
func TestDecodeOpReqImportRejectsPathSeparator(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpReqImport, 0))

	payload := make([]byte, domain.BusIDSize)
	copy(payload, "../etc/shadow")
	buf.Write(payload)

	_, err := wire.DecodeOpReqImport(&buf)
	require.Error(t, err,
		"decoder must refuse a wire busid containing path separators")
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
}
