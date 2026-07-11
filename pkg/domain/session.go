// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/netip"
	"time"
)

// SessionID is a 16-byte UUIDv7 identifying a single client connection.
// The UUIDv7 layout (RFC 9562 §5.7) embeds a millisecond Unix timestamp
// in the high 48 bits, giving sessions a natural chronological ordering.
type SessionID [16]byte

// UUIDv7 layout constants (RFC 9562 §5.7). Naming each masked
// nibble / mask explicitly defeats both `mnd` (magic number) and
// the future-reader question "why 0x0f here, not 0x07?".
const (
	// uuidVersionShift positions the version nibble in the high
	// half of byte 6.
	uuidVersionShift = 32

	// uuidVersion7High is the version-7 nibble pre-shifted into the
	// high half of byte 6 (`0x70` = 0b0111_0000).
	uuidVersion7High = 0x70

	// uuidVersionByteMask preserves the low nibble of byte 6
	// (rand_a high bits) when overlaying the version nibble.
	uuidVersionByteMask = 0x0f

	// uuidVariantRFC4122High is the RFC 4122 / 9562 variant pattern
	// pre-positioned in the top 2 bits of byte 8 (`0x80` = 0b10).
	uuidVariantRFC4122High = 0x80

	// uuidVariantByteMask preserves the low 6 bits of byte 8
	// (rand_b high bits) when overlaying the variant pattern.
	uuidVariantByteMask = 0x3f

	// uuidTSBytesHigh is the count of high-order timestamp bytes
	// (uint16's worth of the 48-bit ms timestamp).
	uuidTSBytesHigh = 2

	// uuidTSBytesLow is the count of low-order timestamp bytes
	// (uint32's worth of the 48-bit ms timestamp).
	uuidTSBytesLow = 4

	// uuidLowMask16 / uuidLowMask32 are explicit truncation masks
	// applied before the uint64 → uint16 / uint32 conversions to
	// make the narrowing intent visible to gosec G115 instead of
	// requiring a //nolint annotation.
	uuidLowMask16 = 0xffff
	uuidLowMask32 = 0xffffffff
)

// String() byte offsets for the canonical 8-4-4-4-12 hex form.
const (
	uuidStrHyphen1 = 8
	uuidStrHyphen2 = 13
	uuidStrHyphen3 = 18
	uuidStrHyphen4 = 23
	uuidStrEnd     = 36
)

// NewSessionID returns a freshly-generated UUIDv7. It returns an error
// when the underlying crypto/rand source fails.
//
// Layout per RFC 9562 §5.7:
//
//	bytes 0..5  unix_ts_ms (48 bits, big-endian)
//	byte  6     version 7 in high nibble + rand_a[0..3] in low nibble
//	byte  7     rand_a[4..11]
//	byte  8     variant `10` in top 2 bits + rand_b[0..5]
//	bytes 9..15 rand_b[6..69]
//
// Implemented inline against crypto/rand instead of a third-party UUID
// package so pkg/domain stays a pure-stdlib value-object surface (the
// invariant the Bazel-backed lint suite protects).
func NewSessionID() (SessionID, error) {
	return newSessionID(rand.Reader, time.Now())
}

func newSessionID(random io.Reader, now time.Time) (SessionID, error) {
	var id SessionID

	_, err := io.ReadFull(random, id[:])
	if err != nil {
		return SessionID{}, fmt.Errorf("generate session id: %w", err)
	}

	// Stamp the millisecond Unix timestamp into the top 48 bits.
	// time.Now().UnixMilli() is int64; in practice (post-1970) it
	// is always positive, but converting to uint64 BEFORE any
	// shift/mask keeps the narrowing arithmetic provably-safe and
	// avoids the implementation-defined behaviour of right-shifting
	// a negative signed value. Splitting the 48 bits into a uint16
	// + uint32 makes the byte slicing explicit without depending on
	// the host's endianness; the uint16 / uint32 conversions are
	// preceded by an explicit low-order mask so gosec G115 sees
	// proven-safe truncation instead of a bare narrow.
	ms := uint64(now.UnixMilli())
	high := uint16((ms >> uuidVersionShift) & uuidLowMask16)
	low := uint32(ms & uuidLowMask32)

	binary.BigEndian.PutUint16(id[0:uuidTSBytesHigh], high)
	binary.BigEndian.PutUint32(id[uuidTSBytesHigh:uuidTSBytesHigh+uuidTSBytesLow], low)

	// Version 7 in high nibble of byte 6, preserving the low nibble
	// (rand_a high bits). Variant `10` in top 2 bits of byte 8,
	// preserving the low 6 bits (rand_b high bits).
	id[6] = (id[6] & uuidVersionByteMask) | uuidVersion7High
	id[8] = (id[8] & uuidVariantByteMask) | uuidVariantRFC4122High

	return id, nil
}

// String returns the canonical 36-character hyphenated UUID form
// (8-4-4-4-12 lowercase hex), matching the output of every
// RFC 9562-conformant UUID library. The five hex segments are
// emitted then the four hyphens overwrite the segment boundaries
// in a separate pass — wsl_v5 dislikes the alternating call /
// assign / call shape that the inline form would produce.
func (id SessionID) String() string {
	// 32 hex chars + 4 hyphens = 36.
	var b [uuidStrEnd]byte

	hex.Encode(b[0:uuidStrHyphen1], id[0:4])
	hex.Encode(b[uuidStrHyphen1+1:uuidStrHyphen2], id[4:6])
	hex.Encode(b[uuidStrHyphen2+1:uuidStrHyphen3], id[6:8])
	hex.Encode(b[uuidStrHyphen3+1:uuidStrHyphen4], id[8:10])
	hex.Encode(b[uuidStrHyphen4+1:uuidStrEnd], id[10:16])

	b[uuidStrHyphen1] = '-'
	b[uuidStrHyphen2] = '-'
	b[uuidStrHyphen3] = '-'
	b[uuidStrHyphen4] = '-'

	return string(b[:])
}

// Session describes a single client connection from the daemon's view.
type Session struct {
	// ID is the unique session identifier (UUIDv7).
	ID SessionID
	// RemoteAddr is the peer address+port as observed by the daemon.
	RemoteAddr netip.AddrPort
	// BusID is the exported device's busid for this session.
	BusID BusID
	// StartedAt is the wall-clock time at which the session handshake completed.
	StartedAt time.Time
	// BytesIn counts bytes received from the peer.
	BytesIn uint64
	// BytesOut counts bytes transmitted to the peer.
	BytesOut uint64
}
