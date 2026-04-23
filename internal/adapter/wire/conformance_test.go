package wire_test

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// upstreamVudcDevice returns the full device descriptor upstream usbipd
// emits for our vudc-bound gadget. Every field is anchored, so any
// single-byte decoder drift fails the test.
func upstreamVudcDevice() domain.Device {
	return domain.Device{
		Path:          "/sys/devices/platform/usbip-vudc.0",
		BusID:         domain.BusID("usbip-vudc.0"),
		BusNum:        0,
		DevNum:        0,
		Speed:         domain.SpeedHigh,
		VendorID:      0x0951,
		ProductID:     0x1666,
		BcdDevice:     0x0110,
		Class:         0,
		Subclass:      0,
		Protocol:      0,
		ConfigValue:   0,
		NumConfigs:    1,
		NumInterfaces: 0,
		Interfaces:    nil,
	}
}

// Upstream-captured fixtures from usbip-utils 2.0 on Linux 6.17.0-20
// against a usbip-vudc.0 gadget (Kingston DataTraveler 0951:1666).
// Regenerate via scripts/capture-wire-fixtures.sh.
//
// Hex is inlined (not loaded from testdata) so the repo never ships
// binary fixtures; mustDecodeHex strips whitespace so the strings can
// be reformatted freely without breaking decoding.
const (
	upstreamOpReqDevlistHex = `
		0111800500000000
	`

	upstreamOpReqImportHex = `
		011180030000000075736269702d767564632e30000000000000000000000000
		0000000000000000
	`

	upstreamOpRepDevlistHex = `
		0111000500000000000000012f7379732f646576696365732f706c6174666f72
		6d2f75736269702d767564632e30000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		00000000000000000000000075736269702d767564632e300000000000000000
		0000000000000000000000000000000000000000000000030951166601100000
		00000100
	`

	upstreamOpRepImportHex = `
		01110003000000002f7379732f646576696365732f706c6174666f726d2f7573
		6269702d767564632e3000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		000000000000000075736269702d767564632e30000000000000000000000000
		0000000000000000000000000000000000000003095116660110000000000100
	`
)

func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()

	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	require.NoError(t, err)

	return b
}

func TestConformance_OpReqDevlist(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpReqDevlistHex)
	require.Len(t, raw, 8)

	version, op, status, err := wire.DecodeHeader(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, domain.ProtocolVersion, version)
	require.Equal(t, wire.OpReqDevlist, op)
	require.Equal(t, uint32(0), status)
}

func TestConformance_OpReqImport(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpReqImportHex)
	require.Len(t, raw, 40)

	busid, err := wire.DecodeOpReqImport(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, domain.BusID("usbip-vudc.0"), busid)
}

func TestConformance_OpRepDevlist(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpRepDevlistHex)
	require.Len(t, raw, 324)

	devices, trailing, err := wire.DecodeOpRepDevlist(bytes.NewReader(raw))
	require.NoError(t, err)
	require.False(t, trailing.TrailingBytes)
	require.Len(t, devices, 1)
	require.Equal(t, upstreamVudcDevice(), devices[0])

	// Round-trip: re-encoding the decoded devices MUST reproduce the
	// exact captured bytes. A byte-for-byte match proves (a) the inline
	// hex has no transcription error, and (b) the codec's encoder and
	// decoder agree on every field offset, endianness, and padding
	// choice. Any single-bit drift in either direction would fail this
	// assertion while the individual field checks above could still
	// pass.
	var roundTrip bytes.Buffer

	require.NoError(t, wire.EncodeOpRepDevlist(&roundTrip, devices))
	require.Equal(t, raw, roundTrip.Bytes())
}

func TestConformance_OpRepImport(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpRepImportHex)
	require.Len(t, raw, 320)

	d, _, err := wire.DecodeOpRepImport(bytes.NewReader(raw))
	require.NoError(t, err)
	// OP_REP_IMPORT does not carry the interfaces array, so the decoded
	// Device has a nil Interfaces slice even though NumInterfaces is a
	// per-device count; the rest of the struct matches the devlist
	// reply byte-for-byte.
	require.Equal(t, upstreamVudcDevice(), d)

	var roundTrip bytes.Buffer

	require.NoError(t, wire.EncodeOpRepImport(&roundTrip, d))
	require.Equal(t, raw, roundTrip.Bytes())
}

func TestConformance_OpReqImportRoundTrip(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpReqImportHex)

	busid, err := wire.DecodeOpReqImport(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, domain.BusID("usbip-vudc.0"), busid)

	var roundTrip bytes.Buffer

	require.NoError(t, wire.EncodeOpReqImport(&roundTrip, busid))
	require.Equal(t, raw, roundTrip.Bytes())
}

func TestConformance_OpReqDevlistRoundTrip(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpReqDevlistHex)
	require.Equal(t, raw, wire.EncodeOpReqDevlist())
}
