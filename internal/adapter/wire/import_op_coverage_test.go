// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestDecodeOpRepImport_DeviceBodyError pins the post-status-OK
// device-body decode error path: when the header parses cleanly with
// status=0 but the device body is truncated, DecodeOpRepImport must
// surface the body-decode error rather than panicking or returning a
// half-populated Device.
func TestDecodeOpRepImport_DeviceBodyError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	// Status-OK header (version=0x0111, op=OpRepImport, status=0).
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint16(0x0111)))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint16(wire.OpRepImport)))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(0)))
	// Truncated body — fewer bytes than DecodeDevice expects.
	buf.Write([]byte{0x01, 0x02, 0x03})

	_, _, err := wire.DecodeOpRepImport(&buf)
	require.Error(t, err)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF,
		"truncated OP_REP_IMPORT body must surface the decode error verbatim")
}

// TestDecodeOpRepImport_UnknownStatus_DefaultsToNotFound pins the
// forward-compatible fallback in mapImportStatus: any status code
// outside the four upstream usbip_common.h values defaults to
// ErrDeviceNotFound rather than ErrProtocolError. Without this
// branch covered, a kernel that introduces a new status code would
// surface as a wire framing fault and obscure the peer rejection.
func TestDecodeOpRepImport_UnknownStatus_DefaultsToNotFound(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint16(0x0111)))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint16(wire.OpRepImport)))
	require.NoError(t, binary.Write(&buf, binary.BigEndian, uint32(99)))

	_, _, err := wire.DecodeOpRepImport(&buf)
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrDeviceNotFound,
		"unknown status code must default to ErrDeviceNotFound (forward-compatible fallback)")
}
