// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestDecodeOpRepDevlistRejectsUnboundedCount pins the devlist-count
// DoS guard: an OP_REP_DEVLIST header whose device count declares
// near-MaxUint32
// must NOT be honoured by allocating a matching []domain.Device
// slice. A hostile peer sending count=0x7FFFFFFF would otherwise OOM
// the process at make-slice time; DecodeOpRepDevlist must reject the
// reply with domain.ErrProtocolMismatch before touching the allocator.
func TestDecodeOpRepDevlistRejectsUnboundedCount(t *testing.T) {
	t.Parallel()

	// OP_REP_DEVLIST header + declared count=0x7FFFFFFF. No body —
	// the decoder must reject BEFORE attempting to decode devices.
	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))

	countBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(countBuf, 0x7FFFFFFF)

	buf.Write(countBuf)

	_, _, err := wire.DecodeOpRepDevlist(&buf)
	require.ErrorIs(t, err, domain.ErrProtocolMismatch,
		"peer-supplied count past the cap must be a protocol violation, not an OOM")
}
