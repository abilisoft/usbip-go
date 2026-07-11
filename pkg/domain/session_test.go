// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// uuidVersionByte is the byte index of the version nibble in a
// standard RFC-9562 UUID layout (time_hi_and_version, high nibble).
const uuidVersionByte = 6

// uuidVariantByte is the byte index whose top 2 bits encode the
// RFC 4122 / 9562 variant (`10` = standard).
const uuidVariantByte = 8

func TestNewSessionID_IsUUIDv7(t *testing.T) {
	t.Parallel()

	id, err := domain.NewSessionID()
	require.NoError(t, err)
	// RFC 9562: version is the top nibble of byte 6.
	version := id[uuidVersionByte] >> 4
	require.Equal(t, byte(7), version, "expected UUIDv7 version nibble")
}

// TestNewSessionID_VariantIsRFC4122 pins the variant bits per RFC 9562
// domain-model OpenSpec: the top two bits of byte 8 MUST be `10`. Without this check
// the stdlib re-implementation could leave random bits in those slots
// and produce technically-not-a-UUID values that consumers using
// strict UUID libraries would reject.
func TestNewSessionID_VariantIsRFC4122(t *testing.T) {
	t.Parallel()

	id, err := domain.NewSessionID()
	require.NoError(t, err)

	variant := id[uuidVariantByte] >> 6
	require.Equal(t, byte(0b10), variant,
		"top 2 bits of byte 8 MUST be `10` (RFC 4122 variant)")
}

// TestNewSessionID_TimestampIsRecent pins the UUIDv7 timestamp
// embedding (RFC 9562 §5.7): the high 48 bits of the ID are a
// millisecond Unix timestamp, big-endian. We allow ±5 s skew vs
// time.Now() to absorb test-runner scheduling jitter; tighter
// skew would risk flake without adding signal.
func TestNewSessionID_TimestampIsRecent(t *testing.T) {
	t.Parallel()

	const (
		skew     int64 = 5_000 // ±5 s in ms.
		ts48Mask       = uint64(1)<<48 - 1
	)

	before := time.Now().UnixMilli()

	id, err := domain.NewSessionID()
	require.NoError(t, err)

	after := time.Now().UnixMilli()

	// High 48 bits, big-endian — pack the six leading bytes back
	// into a uint64 and mask to make the comparison-with-int64
	// safe (uint64 → int64 conversion only narrows when the high
	// 16 bits are zero, which the mask guarantees).
	hi := (uint64(id[0])<<40 | uint64(id[1])<<32 |
		uint64(id[2])<<24 | uint64(id[3])<<16 |
		uint64(id[4])<<8 | uint64(id[5])) & ts48Mask
	hiSigned := int64(hi)

	require.GreaterOrEqual(t, hiSigned, before-skew,
		"UUIDv7 timestamp must not be earlier than the call site")
	require.LessOrEqual(t, hiSigned, after+skew,
		"UUIDv7 timestamp must not exceed wall clock at call return")
}

func TestNewSessionID_Distinct(t *testing.T) {
	t.Parallel()

	a, err := domain.NewSessionID()
	require.NoError(t, err)

	b, err := domain.NewSessionID()
	require.NoError(t, err)
	require.NotEqual(t, a, b)
}

func TestSessionID_String(t *testing.T) {
	t.Parallel()

	id, err := domain.NewSessionID()
	require.NoError(t, err)

	s := id.String()
	require.Len(t, s, 36)
	require.Equal(t, 4, strings.Count(s, "-"))
}

func TestSessionID_ZeroValueString(t *testing.T) {
	t.Parallel()

	var zero domain.SessionID
	require.Equal(t, "00000000-0000-0000-0000-000000000000", zero.String())
}
