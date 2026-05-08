package wire_test

import (
	"bytes"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestDecodeOpRepImportStatusNonZeroIsDeviceNotFound pins the RANK 5
// classification fix: an OP_REP_IMPORT reply whose header status is
// non-zero is a legitimate peer rejection (device unavailable / busid
// not found / busy), NOT a protocol-level failure. The caller must
// observe ErrDeviceNotFound so Importer.Attach can surface the
// documented domain sentinel to its caller instead of an opaque
// "protocol error reported by peer" wrap.
func TestDecodeOpRepImportStatusNonZeroIsDeviceNotFound(t *testing.T) {
	t.Parallel()

	// Minimal header bytes: version=0x0111, op=OpRepImport (0x0003),
	// status=1. No body — the decoder must reject on status before
	// attempting to decode the device.
	buf := []byte{0x01, 0x11, 0x00, 0x03, 0, 0, 0, 1}

	_, err := wire.DecodeOpRepImport(bytes.NewReader(buf))
	require.ErrorIs(t, err, domain.ErrDeviceNotFound,
		"non-zero reply status is the peer saying 'device not available', not a wire framing fault")
}
