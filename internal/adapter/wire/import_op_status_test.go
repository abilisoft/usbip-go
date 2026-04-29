// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestDecodeOpRepImportStatusNonZeroIsDeviceNotFound pins the
// legacy contract: any non-zero reply status is a domain-level peer
// rejection, not a wire framing fault. Kept as the catch-all
// regression — even an unknown future status code must surface as
// ErrDeviceNotFound rather than ErrProtocolError.
func TestDecodeOpRepImportStatusNonZeroIsDeviceNotFound(t *testing.T) {
	t.Parallel()

	buf := []byte{0x01, 0x11, 0x00, 0x03, 0, 0, 0, 1}

	_, _, err := wire.DecodeOpRepImport(bytes.NewReader(buf))
	require.ErrorIs(t, err, domain.ErrDeviceNotFound,
		"non-zero reply status is the peer saying 'device not available', not a wire framing fault")
}

// TestDecodeOpRepImport_StatusMapping pins the per-status sentinel
// mapping required by upstream usbip_common.h:
//
//	ST_OK         = 0  → success, decode device body
//	ST_NA         = 1  → ErrDeviceNotFound (not exported / unknown)
//	ST_DEV_BUSY   = 2  → ErrDeviceAlreadyBound (already in use)
//	ST_DEV_ERR    = 3  → ErrDeviceUnavailable (stub-side internal error)
//	ST_NODEV      = 4  → ErrDeviceNotFound (no such device on remote)
//
// Without the mapping operators see "device not found" for all
// failures — masking the difference between "use a different busid"
// (NODEV) and "wait for the current attacher to detach" (BUSY) and
// "try a different remote" (DEV_ERR).
func TestDecodeOpRepImport_StatusMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status uint32
		want   error
	}{
		{"ST_NA → ErrDeviceNotFound", 1, domain.ErrDeviceNotFound},
		{"ST_DEV_BUSY → ErrDeviceAlreadyBound", 2, domain.ErrDeviceAlreadyBound},
		{"ST_DEV_ERR → ErrDeviceUnavailable", 3, domain.ErrDeviceUnavailable},
		{"ST_NODEV → ErrDeviceNotFound", 4, domain.ErrDeviceNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// OP_REP_IMPORT header: version=0x0111, op=OpRepImport, status.
			var buf bytes.Buffer

			require.NoError(t, binary.Write(&buf, binary.BigEndian, uint16(0x0111)))
			require.NoError(t, binary.Write(&buf, binary.BigEndian, uint16(wire.OpRepImport)))
			require.NoError(t, binary.Write(&buf, binary.BigEndian, tc.status))

			_, _, err := wire.DecodeOpRepImport(&buf)
			require.Error(t, err)
			require.ErrorIs(t, err, tc.want,
				"status %d must map to %v", tc.status, tc.want)
		})
	}
}
