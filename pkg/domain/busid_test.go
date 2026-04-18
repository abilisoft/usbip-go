package domain_test

import (
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestParseBusID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want domain.BusID
		err  bool
	}{
		{"simple", "1-1", domain.BusID("1-1"), false},
		{"nested", "1-1.2", domain.BusID("1-1.2"), false},
		{"max_length", strings.Repeat("a", 31), domain.BusID(strings.Repeat("a", 31)), false},
		{"empty", "", "", true},
		{"whitespace", " ", "", true},
		{"over_limit", strings.Repeat("a", 32), "", true},
		{"contains_null", "1-\x00", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := domain.ParseBusID(tc.in)
			if tc.err {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			require.True(t, got.IsValid())
			require.Equal(t, string(tc.want), got.String())
		})
	}
}

func TestBusID_IsValid_ZeroValue(t *testing.T) {
	t.Parallel()

	var b domain.BusID
	require.False(t, b.IsValid())
}
