package wire_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestDecodeOpReqImportRejectsInvalidBusID pins the contract that a
// peer-supplied busid must satisfy the domain's topology pattern
// before DecodeOpReqImport returns it. An exporter serving untrusted
// peers otherwise hands a garbage string to the sysfs layer, which
// the kernel bind step rejects with a far less useful error well
// downstream of the protocol entry point.
func TestDecodeOpReqImportRejectsInvalidBusID(t *testing.T) {
	t.Parallel()

	// Build the header by hand, then a 32-byte NUL-padded payload that
	// does not satisfy the BusID topology pattern. "not a busid" has
	// printable ASCII so it will round-trip through any NUL-scan
	// decoder; the topology-pattern check is the only gate.
	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpReqImport, 0))

	payload := make([]byte, domain.BusIDSize)
	// Leading whitespace survives ReadPaddedString's printable scan but
	// yields an ambiguous busid for every downstream helper (sysfs
	// lookup, log correlation, metrics cardinality). The wire-level
	// validator must reject it with ErrBusIDInvalid so the peer learns
	// the input was malformed rather than the request succeeding
	// against whatever whitespace-prefixed busid happens to match.
	copy(payload, " 1-1")
	buf.Write(payload)

	_, err := wire.DecodeOpReqImport(&buf)
	require.Error(t, err,
		"decoder must reject a peer-supplied busid with leading whitespace")
	require.ErrorIs(t, err, domain.ErrBusIDInvalid,
		"rejection must classify as ErrBusIDInvalid so callers can branch correctly")
}
