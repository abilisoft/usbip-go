// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestDefaultTimeouts_ExactValues pins the three default timeout
// constants. ARITHMETIC_BASE mutates the multiplicand (e.g. `10`
// in `10 * time.Second`); without a direct value assertion the
// mutants survive because callers using these defaults rarely
// observe a specific number.
func TestDefaultTimeouts_ExactValues(t *testing.T) {
	t.Parallel()

	require.Equal(t, 10*time.Second, domain.DefaultDialTimeout,
		"DefaultDialTimeout must be 10s; ARITHMETIC_BASE on the multiplicand changes the perceived budget")
	require.Equal(t, 5*time.Second, domain.DefaultHandshakeTimeout,
		"DefaultHandshakeTimeout must be 5s")
	require.Equal(t, 30*time.Second, domain.DefaultShutdownTimeout,
		"DefaultShutdownTimeout must be 30s")
}

// TestParseRemote_BracketedIPv6Variants pins the three uncovered
// switch arms in splitBracketedIPv6:
//
//   - rest == "" → host, "", nil (just "[host]")
//   - rest starts with ":" → host, rest[1:], nil ("[host]:port")
//   - anything else → error
func TestParseRemote_BracketedIPv6Variants(t *testing.T) {
	t.Parallel()

	t.Run("bracketed_no_port_uses_default", func(t *testing.T) {
		t.Parallel()

		got, err := domain.ParseRemote("[::1]")
		require.NoError(t, err,
			"bracketed IPv6 with no port falls through to DefaultPort")
		require.Equal(t, "::1", got.Host)
	})

	t.Run("bracketed_with_port", func(t *testing.T) {
		t.Parallel()

		got, err := domain.ParseRemote("[::1]:3240")
		require.NoError(t, err)
		require.Equal(t, "::1", got.Host)
		require.Equal(t, uint16(3240), got.Port)
	})

	t.Run("bracketed_garbage_after", func(t *testing.T) {
		t.Parallel()

		_, err := domain.ParseRemote("[::1]xyz")
		require.Error(t, err,
			"trailing garbage after closing bracket must be rejected; covers the default-arm of the switch")
	})
}

// TestIsHostLabelChar_Boundaries exercises every accept-set boundary
// in isHostLabelChar. Like isWireBusIDRune in busid.go, the function
// has multiple `>= / <=` pairs that mutate independently; without
// boundary inputs they all survive.
//
// Tests through ParseRemote with a hostname containing each
// boundary char as the only label content.
func TestIsHostLabelChar_Boundaries(t *testing.T) {
	t.Parallel()

	// Every boundary char that MUST be accepted as a hostname label.
	// Single-char labels are valid per RFC 1034 (well, "a" is — we
	// pin the rune-level acceptance).
	accepted := []string{"a", "z", "A", "Z", "0", "9", "a-b"}
	for _, s := range accepted {
		t.Run("accept_"+s, func(t *testing.T) {
			t.Parallel()

			_, err := domain.ParseRemote(s + ":3240")
			require.NoError(t, err,
				"hostname label %q must be accepted; mutation that shifts the boundary one beyond would reject it", s)
		})
	}

	// Just-out-of-range chars MUST be rejected.
	rejected := []string{"`", "{", "@", "[", "/", ":"}
	for _, s := range rejected {
		t.Run("reject_"+s, func(t *testing.T) {
			t.Parallel()

			_, err := domain.ParseRemote(s + "x:3240")
			require.Error(t, err,
				"just-out-of-range hostname rune %q must be rejected", s)
		})
	}
}

// TestParseRemote_ColonAtZeroIndexEmptyHost specifically targets
// the LIVED CONDITIONALS_BOUNDARY at remote.go:139 by submitting
// ":3240" — strings.LastIndexByte returns 0 here, the only input
// that distinguishes `idx >= 0` from `idx > 0`. Real splits to
// (host="", port="3240") and validateHost rejects empty host.
// Mutant `idx > 0` falls through to bare-host branch and parses
// ":3240" as a host name, which must ALSO error — but with a
// different error class. The differentiator is the error
// message naming "empty" host vs "invalid character" host.
func TestParseRemote_ColonAtZeroIndexEmptyHost(t *testing.T) {
	t.Parallel()

	_, err := domain.ParseRemote(":3240")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty",
		"input with leading colon must surface an empty-host error; "+
			"mutant `>` would treat the whole string as a bare host and surface a character/format error instead")
}
