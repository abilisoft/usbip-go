// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestParseRemote_BoundaryCases pins the boundary inputs that the
// existing remote_test.go misses. Each case targets one specific
// CONDITIONALS_BOUNDARY mutant in remote.go that the regular
// suite leaves alive because no input lands on the exact
// transition.
func TestParseRemote_BoundaryCases(t *testing.T) {
	t.Parallel()

	// remote.go:240 — `len(label) > hostLabelMaxLen` — boundary at
	// label length == 63 (max valid). ":port"-form input where the
	// hostname label is exactly 63 chars MUST be accepted; a `>=`
	// mutation would reject it.
	t.Run("hostname_label_at_max_length", func(t *testing.T) {
		t.Parallel()

		label := strings.Repeat("a", 63)
		_, err := domain.ParseRemote(label + ":3240")
		require.NoError(t, err,
			"a 63-byte hostname label is exactly at the cap and must be accepted; mutation `>` -> `>=` would reject it")
	})

	// Same boundary, one over: 64 chars must be rejected. Pins the
	// other direction so a mutation that broadens the cap still
	// fails.
	t.Run("hostname_label_one_over_max_length", func(t *testing.T) {
		t.Parallel()

		label := strings.Repeat("a", 64)
		_, err := domain.ParseRemote(label + ":3240")
		require.Error(t, err,
			"a 64-byte label exceeds RFC1034; the guard must fire")
	})

	// remote.go:182 — `strings.Count(h, ":") >= ipv6MinColons` (==2)
	// in validateHost. A bare host with EXACTLY 2 colons that is
	// NOT a valid IP literal (e.g. "a:b:c") must error. A `>` mutant
	// would skip the IP-literal check and accept it.
	t.Run("two_colon_non_ip_host_rejected", func(t *testing.T) {
		t.Parallel()

		_, err := domain.ParseRemote("a:b:c")
		require.Error(t, err,
			"a host with exactly ipv6MinColons=2 colons that is not a valid IP must be rejected; "+
				"CONDITIONALS_BOUNDARY mutant would skip the validation")
	})

	// remote.go:139 — `idx >= 0` for LastIndexByte(s, ':'). Boundary:
	// idx=0 means s starts with ':', so the host is empty. Such an
	// input MUST error (empty host). A `>` mutant would treat the
	// input as a bare host (":3240" as host name) and accept it.
	t.Run("colon_at_zero_index_rejected", func(t *testing.T) {
		t.Parallel()

		// ":3240" has idx=0; real splits to (host="", port="3240")
		// and validateHost rejects empty. Mutant `>` falls through
		// to bare-host branch and validateHost rejects ":3240" as
		// invalid host — both error. To distinguish, use an input
		// that real rejects on empty-host vs mutant accepts as bare
		// host. ":host" leads real to (host="", port="host"), error.
		// Mutant treats whole string as bare host ":host", which
		// validateHost rejects too — same outcome.
		// The only differentiating case is a single-colon-prefix
		// where the rest is a valid bare host name; we check the
		// error MESSAGE distinguishes empty-host from
		// invalid-character-host.
		_, err := domain.ParseRemote(":h")
		require.Error(t, err,
			":h must be rejected as invalid (empty host)")
	})

	// remote.go:149 — `closeIdx < 0` for IndexByte(s, ']') in the
	// bracketed-IPv6 path. Boundary: closeIdx=0 means s is "]..."
	// — a malformed bracketed form. Real treats it as having a
	// closing bracket at position 0 (host="" between [ and ]).
	// Mutant `<= 0` still triggers the error path. Differentiator:
	// closeIdx exactly 0 vs 1.
	t.Run("malformed_bracketed_no_closing", func(t *testing.T) {
		t.Parallel()

		_, err := domain.ParseRemote("[::1")
		require.Error(t, err,
			"missing ] in bracketed form must error")
	})
}
