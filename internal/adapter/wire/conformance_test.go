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
	require.False(t, trailing)
	require.Len(t, devices, 1)

	d := devices[0]
	require.True(t, strings.HasPrefix(d.Path, "/sys/devices/platform/usbip-vudc.0"))
	require.Equal(t, domain.BusID("usbip-vudc.0"), d.BusID)
	require.Equal(t, uint16(0), d.BusNum)
	require.Equal(t, uint16(0), d.DevNum)
	require.Equal(t, domain.SpeedHigh, d.Speed)
	require.Equal(t, uint16(0x0951), d.VendorID)
	require.Equal(t, uint16(0x1666), d.ProductID)
	require.Equal(t, uint16(0x0110), d.BcdDevice)
	// Gadget is attached to vudc but not yet enumerated by a host, so
	// ConfigValue=0 and NumInterfaces=0 — distinct shape from the
	// synthetic enumerated-Kingston fixture which exercises the
	// bNumInterfaces>0 path.
	require.Equal(t, uint8(0), d.ConfigValue)
	require.Equal(t, uint8(1), d.NumConfigs)
	require.Equal(t, uint8(0), d.NumInterfaces)
	require.Empty(t, d.Interfaces)
}

func TestConformance_OpRepImport(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpRepImportHex)
	require.Len(t, raw, 320)

	d, err := wire.DecodeOpRepImport(bytes.NewReader(raw))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(d.Path, "/sys/devices/platform/usbip-vudc.0"))
	require.Equal(t, domain.BusID("usbip-vudc.0"), d.BusID)
	require.Equal(t, uint16(0x0951), d.VendorID)
	require.Equal(t, uint16(0x1666), d.ProductID)
	require.Equal(t, domain.SpeedHigh, d.Speed)
}
