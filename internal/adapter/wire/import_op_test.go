// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"bytes"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestOpReqImportRoundTrip encodes and decodes a request carrying a
// busid; the decoded busid must match the input.
func TestOpReqImportRoundTrip(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := wire.EncodeOpReqImport(&buf, domain.BusID("1-1"))
	require.NoError(t, err)

	got, err := wire.DecodeOpReqImport(&buf)
	require.NoError(t, err)
	require.Equal(t, domain.BusID("1-1"), got)
}

// TestOpReqImportBusIDOverflow rejects too-long busids at encode.
func TestOpReqImportBusIDOverflow(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := wire.EncodeOpReqImport(&buf, domain.BusID(bytes.Repeat([]byte{'x'}, domain.BusIDSize)))
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
}

// TestOpRepImportRoundTrip encodes a success reply (status=0) with a
// device body; decoder returns the device verbatim.
func TestOpRepImportRoundTrip(t *testing.T) {
	t.Parallel()

	dev := domain.Device{
		Path:      "/sys/foo",
		BusID:     domain.BusID("1-1"),
		BusNum:    1,
		DevNum:    2,
		Speed:     domain.SpeedHigh,
		VendorID:  0x1234,
		ProductID: 0x5678,
		BcdDevice: 0x0100,
	}

	var buf bytes.Buffer

	require.NoError(t, wire.EncodeOpRepImport(&buf, dev))

	got, _, err := wire.DecodeOpRepImport(&buf)
	require.NoError(t, err)
	require.Equal(t, dev.Path, got.Path)
	require.Equal(t, dev.BusID, got.BusID)
	require.Equal(t, dev.Speed, got.Speed)
	require.Equal(t, dev.VendorID, got.VendorID)
}

// TestOpRepImportStatusError decodes a reply whose status is non-zero.
// Per spec §6.2, a non-zero OP_REP_IMPORT status means the peer
// rejected the import — device unavailable / busy / not found. The
// decoder surfaces domain.ErrDeviceNotFound so callers classify the
// rejection as a domain-level signal rather than a wire framing
// error (the generic DecodeHeader reply-status check would otherwise
// misclassify this as ErrProtocolError).
func TestOpRepImportStatusError(t *testing.T) {
	t.Parallel()

	// Build a header with status=1 (OP_REP_IMPORT failure).
	buf := []byte{0x01, 0x11, 0x00, 0x03, 0, 0, 0, 1}

	_, _, err := wire.DecodeOpRepImport(bytes.NewReader(buf))
	require.ErrorIs(t, err, domain.ErrDeviceNotFound)
}
