package domain_test

import (
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

	require.Equal(t, "host:3240", domain.RemoteEndpoint{Host: "host"}.String())
	require.Equal(t, "host:1234", domain.RemoteEndpoint{Host: "host", Port: 1234}.String())
	require.Equal(t, "[::1]:3240", domain.RemoteEndpoint{Host: "::1"}.String())
	require.Equal(t, "[::1]:1234", domain.RemoteEndpoint{Host: "::1", Port: 1234}.String())
}

func TestParseRemote(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want domain.RemoteEndpoint
		err  bool
	}{
		{"host_only", "host", domain.RemoteEndpoint{Host: "host", Port: 3240}, false},
		{"host_port", "host:1234", domain.RemoteEndpoint{Host: "host", Port: 1234}, false},
		{"ipv6_bracket_port", "[::1]:1234", domain.RemoteEndpoint{Host: "::1", Port: 1234}, false},
		{"ipv4_port", "1.2.3.4:5", domain.RemoteEndpoint{Host: "1.2.3.4", Port: 5}, false},
		{"empty", "", domain.RemoteEndpoint{}, true},
		{"whitespace", "   ", domain.RemoteEndpoint{}, true},
		{"bad_port", "host:abc", domain.RemoteEndpoint{}, true},
		{"port_overflow", "host:99999", domain.RemoteEndpoint{}, true},
		{"port_zero", "host:0", domain.RemoteEndpoint{}, true},
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
