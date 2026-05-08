//go:build conformance_linux

package conformance_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/abilisoft/usbip-go/test/conformance"
	"github.com/stretchr/testify/require"
)

// upstreamOpRepDevlistHex is the ground-truth OP_REP_DEVLIST bytes
// captured from real upstream usbipd 2.0 against a usbip-vudc.0
// gadget advertising Kingston DataTraveler 0951:1666. Mirrors the
// fixture used by internal/adapter/wire/conformance_test.go so a
// single byte drift in either encoder or decoder fails across the
// stack. No binary files are committed per project policy; whitespace
// is stripped at decode time so the hex can be reformatted freely.
const upstreamOpRepDevlistHex = `
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

// upstreamOpRepImportHex is the ground-truth OP_REP_IMPORT reply for
// a successful import of the same Kingston vudc gadget.
const upstreamOpRepImportHex = `
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

// upstreamVudcDevice returns the domain.Device the fixtures encode.
// Duplicated from internal/adapter/wire/conformance_test.go because
// test helpers from internal packages aren't importable here; the
// expected bytes are fully under this test's control.
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
	}
}

// mustDecodeHex strips whitespace from s and returns the decoded
// byte slice. Fails the test on invalid hex.
func mustDecodeHex(t *testing.T, s string) []byte {
	t.Helper()

	b, err := hex.DecodeString(strings.Join(strings.Fields(s), ""))
	require.NoError(t, err)

	return b
}

// TestConformanceGoClientListRemote drives usbip.Importer.ListRemote
// against the synthetic upstream and asserts the returned device
// matches the fixture byte-for-byte. Proves our decoder interprets
// an upstream-captured OP_REP_DEVLIST identically to what upstream
// produced.
func TestConformanceGoClientListRemote(t *testing.T) {
	t.Parallel()

	upstream, addr, err := conformance.StartSyntheticUpstream(upstreamVudcDevice())
	require.NoError(t, err)

	t.Cleanup(func() { _ = upstream.Close() })

	imp, err := usbip.NewImporter()
	require.NoError(t, err)

	t.Cleanup(func() { _ = imp.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	devices, err := imp.ListRemote(ctx, addr)
	require.NoError(t, err)
	require.Len(t, devices, 1)
	require.Equal(t, upstreamVudcDevice(), devices[0])

	// Round-trip: re-encoding the decoded device via our encoder
	// MUST reproduce the captured bytes byte-for-byte. Asserts the
	// encoder and decoder agree on every field.
	var buf bytes.Buffer

	err = wire.EncodeOpRepDevlist(&buf, devices)
	require.NoError(t, err)

	wantRaw := mustDecodeHex(t, upstreamOpRepDevlistHex)

	require.Equal(t, wantRaw, buf.Bytes(),
		"encoder must reproduce the fixture bytes")
}

// TestConformanceGoClientListRemoteByteMutationDetected proves the
// test actually catches byte-level deviation: we mutate a single
// byte of the fixture, replay, and expect the resulting device's
// VendorID to differ so the assertion would fire. This is a
// sanity-check that our round-trip fixture is actually load-bearing.
func TestConformanceGoClientListRemoteByteMutationDetected(t *testing.T) {
	t.Parallel()

	// Decode the original fixture, mutate one byte inside the
	// vendor-id little-endian slot, and assert the decoded device
	// differs from the upstream canonical.
	raw := mustDecodeHex(t, upstreamOpRepDevlistHex)

	// VendorID in OP_REP_DEVLIST lives at fixture offset 312:
	//   8 (op header) + 4 (ndevs) + 300 (offDevVendorID within
	//   the 312-byte device descriptor) = 312. Flipping the low
	//   byte must produce a device whose VendorID no longer
	//   equals the canonical 0x0951.
	const vendorIDHiOffset = 8 + 4 + 300

	mutated := make([]byte, len(raw))
	copy(mutated, raw)
	mutated[vendorIDHiOffset] ^= 0x0F // flip a few low bits of the high byte

	codec := &wire.Codec{}

	devs, err := codec.DecodeOpRepDevlist(bytes.NewReader(mutated))
	require.NoError(t, err)
	require.Len(t, devs, 1)
	require.NotEqual(t, upstreamVudcDevice().VendorID, devs[0].VendorID,
		"a single-byte fixture mutation MUST produce a distinguishable device")
}

// TestConformanceGoClientOpRepImport exercises the second half of
// the handshake: start the synthetic upstream, open a raw TCP conn
// (since Attach needs the kernel-side AttachRemote which is out of
// scope for hosted CI), write OP_REQ_IMPORT, read OP_REP_IMPORT,
// decode, and compare to the fixture. This mirrors what Attach's
// handshake does up to the sysfs handoff without touching the
// kernel.
func TestConformanceGoClientOpRepImport(t *testing.T) {
	t.Parallel()

	upstream, addr, err := conformance.StartSyntheticUpstream(upstreamVudcDevice())
	require.NoError(t, err)

	t.Cleanup(func() { _ = upstream.Close() })

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(addr.Host, portStr(addr.Port)), 2*time.Second)
	require.NoError(t, err)

	t.Cleanup(func() { _ = conn.Close() })

	err = wire.EncodeOpReqImport(conn, upstreamVudcDevice().BusID)
	require.NoError(t, err)

	dev, err := wire.DecodeOpRepImport(conn)
	require.NoError(t, err)
	require.Equal(t, upstreamVudcDevice(), dev)

	// Round-trip byte compare against the captured fixture.
	var buf bytes.Buffer

	err = wire.EncodeOpRepImport(&buf, dev)
	require.NoError(t, err)

	wantRaw := mustDecodeHex(t, upstreamOpRepImportHex)

	require.Equal(t, wantRaw, buf.Bytes(),
		"Go encoder must reproduce the fixture OP_REP_IMPORT bytes exactly")
}

// portStr renders a uint16 port as decimal for net.JoinHostPort.
// Declared here rather than importing strconv so the test file's
// dependency footprint stays minimal.
func portStr(p uint16) string {
	if p == 0 {
		return "0"
	}

	buf := make([]byte, 0, 5)

	for p > 0 {
		buf = append([]byte{byte('0' + p%10)}, buf...)
		p /= 10
	}

	return string(buf)
}
