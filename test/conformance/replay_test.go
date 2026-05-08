// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build conformance_linux

package conformance_test

import (
	"bytes"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/stretchr/testify/require"
)

// TestConformanceReplayOpReqDevlistRoundTrip captures the 8-byte
// OP_REQ_DEVLIST fixture from upstream usbipd 2.0, re-encodes via our
// codec, and asserts byte-for-byte equivalence. Any drift in the
// encoder's header output (version, opcode, reserved status) would
// fail this round-trip.
//
// Fixtures are inlined as hex per project policy (no binary files
// committed). scripts/capture-wire-fixtures.sh regenerates them
// against a live upstream; the hex blob here is the stable snapshot
// of the fixture taken at the time of capture.
func TestConformanceReplayOpReqDevlistRoundTrip(t *testing.T) {
	t.Parallel()

	const upstreamOpReqDevlistHex = `0111800500000000`

	raw := mustDecodeHex(t, upstreamOpReqDevlistHex)
	require.Len(t, raw, 8)

	// Pure re-encode: EncodeOpReqDevlist is deterministic and takes
	// no arguments, so the output bytes must be identical to the
	// captured fixture.
	got := wire.EncodeOpReqDevlist()

	require.Equal(t, raw, got, "EncodeOpReqDevlist output must reproduce upstream bytes exactly")
}

// TestConformanceReplayOpReqImportRoundTrip exercises the
// OP_REQ_IMPORT decode→encode cycle: the upstream 40-byte frame is
// decoded to a BusID, re-encoded via our encoder, and the result must
// match the fixture byte-for-byte. Mutations of the encoder's header
// or busid padding logic surface here.
func TestConformanceReplayOpReqImportRoundTrip(t *testing.T) {
	t.Parallel()

	const upstreamOpReqImportHex = `
		011180030000000075736269702d767564632e30000000000000000000000000
		0000000000000000
	`

	raw := mustDecodeHex(t, upstreamOpReqImportHex)
	require.Len(t, raw, 40)

	busid, err := wire.DecodeOpReqImport(bytes.NewReader(raw))
	require.NoError(t, err)

	var buf bytes.Buffer

	err = wire.EncodeOpReqImport(&buf, busid)
	require.NoError(t, err)

	require.Equal(t, raw, buf.Bytes(),
		"EncodeOpReqImport must reproduce upstream bytes for decoded BusID %q", busid)
}

// TestConformanceReplayOpRepDevlistRoundTrip decodes the canonical
// OP_REP_DEVLIST bytes into a []domain.Device, re-encodes, and
// asserts byte-for-byte equivalence. Covers: header, device count,
// full per-device descriptor block (path, busid, numeric fields),
// and trailing-bytes rejection.
func TestConformanceReplayOpRepDevlistRoundTrip(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpRepDevlistHex)

	devices, flags, err := wire.DecodeOpRepDevlist(bytes.NewReader(raw))
	require.NoError(t, err)
	require.False(t, flags.TrailingBytes, "fixture must decode without trailing bytes")
	require.Empty(t, flags.TruncatedPaddedStrings,
		"fixture must decode without padded-string truncation")
	require.Len(t, devices, 1)

	var buf bytes.Buffer

	err = wire.EncodeOpRepDevlist(&buf, devices)
	require.NoError(t, err)

	require.Equal(t, raw, buf.Bytes(),
		"re-encoding decoded devices must reproduce the fixture bytes")
}

// TestConformanceReplayOpRepImportRoundTrip mirrors the devlist
// round-trip for the single-device OP_REP_IMPORT body. The decoded
// device's Interfaces slice is nil (OP_REP_IMPORT does not carry
// the interface array), and the encoder handles that correctly in
// its own right; the round-trip proves the handling agrees with
// upstream's encoding.
func TestConformanceReplayOpRepImportRoundTrip(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpRepImportHex)

	dev, _, err := wire.DecodeOpRepImport(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Equal(t, upstreamVudcDevice(), dev)

	var buf bytes.Buffer

	err = wire.EncodeOpRepImport(&buf, dev)
	require.NoError(t, err)

	require.Equal(t, raw, buf.Bytes(),
		"re-encoding decoded device must reproduce the fixture OP_REP_IMPORT bytes")
}

// TestConformanceReplayOpRepImportDecodeMutationDetected proves the
// OP_REP_IMPORT round-trip is load-bearing: flipping a byte inside
// the numeric device block changes the decoded value. This is a
// lock-in against a regression where the encoder or decoder
// quietly zeroes out a field it doesn't understand.
func TestConformanceReplayOpRepImportDecodeMutationDetected(t *testing.T) {
	t.Parallel()

	raw := mustDecodeHex(t, upstreamOpRepImportHex)

	// OP_REP_IMPORT layout: 8 (header) + 312 (device) = 320 bytes.
	// ProductID lives at byte offset 302 within the device, so
	// fixture offset = 8 + 302 = 310. Flip a bit; decoded device
	// must have a different ProductID from the upstream canonical.
	const productIDLoOffset = 8 + 303

	mutated := make([]byte, len(raw))
	copy(mutated, raw)
	mutated[productIDLoOffset] ^= 0x0F

	dev, _, err := wire.DecodeOpRepImport(bytes.NewReader(mutated))
	require.NoError(t, err)
	require.NotEqual(t, upstreamVudcDevice().ProductID, dev.ProductID,
		"single-byte mutation at ProductID offset must produce a distinguishable device")
}
