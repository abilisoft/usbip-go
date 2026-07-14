// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestRemoteEndpoint_NormalizePort(t *testing.T) {
	t.Parallel()

	got := domain.RemoteEndpoint{Host: "h"}.NormalizePort()
	require.Equal(t, uint16(3240), got.Port)
	require.Equal(t, "h", got.Host)

	// Non-zero port is preserved.
	got2 := domain.RemoteEndpoint{Host: "h", Port: 1234}.NormalizePort()
	require.Equal(t, uint16(1234), got2.Port)
}

func TestRemoteEndpoint_String(t *testing.T) {
	t.Parallel()

	require.Equal(t, "host:3240", domain.RemoteEndpoint{Host: testHostLabel}.String())
	require.Equal(t, "host:1234", domain.RemoteEndpoint{Host: testHostLabel, Port: 1234}.String())
	require.Equal(t, "[::1]:3240", domain.RemoteEndpoint{Host: testIPv6Loopback}.String())
	require.Equal(t, "[::1]:1234", domain.RemoteEndpoint{Host: testIPv6Loopback, Port: 1234}.String())
}

func TestParseRemote(t *testing.T) {
	t.Parallel()

	const scopedIPv6 = "fe80::1%eth0"

	hostname253 := strings.Join([]string{
		strings.Repeat("a", 63),
		strings.Repeat("b", 63),
		strings.Repeat("c", 63),
		strings.Repeat("d", 61),
	}, ".")
	hostname254 := hostname253 + "e"

	cases := []struct {
		name string
		in   string
		want domain.RemoteEndpoint
		err  bool
	}{
		{"host_only", testHostLabel, domain.RemoteEndpoint{Host: testHostLabel, Port: 3240}, false},
		{"host_port", "host:1234", domain.RemoteEndpoint{Host: testHostLabel, Port: 1234}, false},
		{"ipv6_bracket_port", "[::1]:1234", domain.RemoteEndpoint{Host: testIPv6Loopback, Port: 1234}, false},
		{"ipv4_port", "1.2.3.4:5", domain.RemoteEndpoint{Host: "1.2.3.4", Port: 5}, false},
		{"empty", "", domain.RemoteEndpoint{}, true},
		{"whitespace", "   ", domain.RemoteEndpoint{}, true},
		{"bad_port", "host:abc", domain.RemoteEndpoint{}, true},
		{"empty_port", "host:", domain.RemoteEndpoint{}, true},
		{"port_overflow", "host:99999", domain.RemoteEndpoint{}, true},
		{"port_zero", "host:0", domain.RemoteEndpoint{}, true},
		{"ipv6_bare", testIPv6Loopback, domain.RemoteEndpoint{Host: testIPv6Loopback, Port: 3240}, false},
		{"ipv6_only_bracketed", "[::1]", domain.RemoteEndpoint{Host: testIPv6Loopback, Port: 3240}, false},
		{"ipv6_bracket_empty_port", "[::1]:", domain.RemoteEndpoint{}, true},
		{"ipv6_scoped_bare", scopedIPv6, domain.RemoteEndpoint{Host: scopedIPv6, Port: 3240}, false},
		{"ipv6_scoped_bracketed", "[fe80::1%eth0]:3241", domain.RemoteEndpoint{Host: scopedIPv6, Port: 3241}, false},
		{"bracketed_ipv4", "[192.0.2.1]:3240", domain.RemoteEndpoint{}, true},
		{"bracketed_hostname", "[host.example]:3240", domain.RemoteEndpoint{}, true},
		{"ipv6_missing_bracket", "[::1", domain.RemoteEndpoint{}, true},
		{"ipv6_bad_suffix", "[::1]garbage", domain.RemoteEndpoint{}, true},
		{"empty_host", ":1234", domain.RemoteEndpoint{}, true},
		// Host-format validation.
		{"host_with_space", "bad host", domain.RemoteEndpoint{}, true},
		{"host_with_tab", "bad\thost", domain.RemoteEndpoint{}, true},
		{"host_with_control", "bad\x01host", domain.RemoteEndpoint{}, true},
		{"ambiguous_multi_colon", "a:b:c", domain.RemoteEndpoint{}, true},
		{"invalid_ipv6_short", "::xyz", domain.RemoteEndpoint{}, true},
		{"host_starts_with_dot", ".host.example", domain.RemoteEndpoint{}, true},
		{"host_trailing_dot_ok", "host.example.", domain.RemoteEndpoint{Host: "host.example.", Port: 3240}, false},
		{"label_too_long", strings.Repeat("a", 64) + ".example", domain.RemoteEndpoint{}, true},
		{"hostname_253_bytes", hostname253, domain.RemoteEndpoint{Host: hostname253, Port: 3240}, false},
		{"hostname_253_bytes_absolute", hostname253 + ".", domain.RemoteEndpoint{Host: hostname253 + ".", Port: 3240}, false},
		{"hostname_254_bytes", hostname254, domain.RemoteEndpoint{}, true},
		{"label_leading_hyphen", "-host.example", domain.RemoteEndpoint{}, true},
		{"label_trailing_hyphen", "host-.example", domain.RemoteEndpoint{}, true},
		{"label_underscore", "bad_host", domain.RemoteEndpoint{}, true},
		{"label_only_dots", "..", domain.RemoteEndpoint{}, true},
		{"bare_dot", ".", domain.RemoteEndpoint{}, true},
		{"valid_with_digits", "host1.example2", domain.RemoteEndpoint{Host: "host1.example2", Port: 3240}, false},
		{"valid_with_hyphens", "a-b-c.d-e-f", domain.RemoteEndpoint{Host: "a-b-c.d-e-f", Port: 3240}, false},
		{"ipv6_full", testIPv6LinkLocal, domain.RemoteEndpoint{Host: testIPv6LinkLocal, Port: 3240}, false},
		{"ipv6_bracket_noport", "[fe80::1]", domain.RemoteEndpoint{Host: testIPv6LinkLocal, Port: 3240}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.ParseRemote(tc.in)
			if tc.err {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
