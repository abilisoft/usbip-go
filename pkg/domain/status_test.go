package domain_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestStatus_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   domain.Status
		want string
	}{
		{"null", domain.StatusNull, "null"},
		{"not_assigned", domain.StatusNotAssigned, "not-assigned"},
		{"available", domain.StatusAvailable, "available"},
		{"used", domain.StatusUsed, "used"},
		{"error", domain.StatusError, "error"},
		{"fallback", domain.Status(99), "status(99)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.in.String())
		})
	}
}

func TestStatus_NumericValues(t *testing.T) {
	t.Parallel()

	require.Equal(t, domain.StatusNull, domain.Status(0))
	require.Equal(t, domain.StatusNotAssigned, domain.Status(1))
	require.Equal(t, domain.StatusAvailable, domain.Status(2))
	require.Equal(t, domain.StatusUsed, domain.Status(3))
	require.Equal(t, domain.StatusError, domain.Status(4))
}
