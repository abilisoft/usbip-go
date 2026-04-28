// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestDecodeOpReqImportBody_ReadsBusIDWithoutHeader pins the
// daemon-side path: handleConn already consumed the 8-byte header
// to dispatch by opcode, so the import body decoder must read ONLY
// the 32-byte busid. Calling the full DecodeOpReqImport here would
// re-read 8 bytes of busid as if they were a header and surface
// ErrProtocolMismatch with the busid's first two bytes as the
// bogus version (0x332d = "3-" → "got version 0x332d want 0x0111"
// against a "3-1" busid).
//
// Regression net for the daemon double-read bug that broke every
// attach call after a fresh deploy.
func TestDecodeOpReqImportBody_ReadsBusIDWithoutHeader(t *testing.T) {
	t.Parallel()

	// 32-byte NUL-padded busid only — no header.
	buf := make([]byte, domain.BusIDSize)
	copy(buf, "3-1")

	got, err := wire.DecodeOpReqImportBody(bytes.NewReader(buf))
	require.NoError(t, err,
		"DecodeOpReqImportBody must succeed on bare busid bytes; the daemon dispatcher already read the header")
	require.Equal(t, domain.BusID("3-1"), got,
		"the parsed busid must match the bytes written, not 0x332d-as-version")
}

// TestDecodeOpReqImport_StillReadsHeader pins that the full
// DecodeOpReqImport entry point continues to consume the 8-byte
// header before the body — needed by callers that own the read
// from raw bytes (tests, library users) and never want to split
// the parse.
func TestDecodeOpReqImport_StillReadsHeader(t *testing.T) {
	t.Parallel()

	full := bytes.Buffer{}
	require.NoError(t, wire.EncodeOpReqImport(&full, domain.BusID("3-1")))

	require.Equal(t, 8+domain.BusIDSize, full.Len(),
		"OP_REQ_IMPORT request is 8-byte header + 32-byte busid")

	got, err := wire.DecodeOpReqImport(&full)
	require.NoError(t, err,
		"DecodeOpReqImport must accept its own EncodeOpReqImport output")
	require.Equal(t, domain.BusID("3-1"), got)
}
