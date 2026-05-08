package domain_test

import (
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// uuidVersionNibbleOffset is the byte index of the version nibble in a
// standard RFC-9562 UUID layout (time_hi_and_version, high nibble).
const uuidVersionByte = 6

func TestNewSessionID_IsUUIDv7(t *testing.T) {
	t.Parallel()

	id, err := domain.NewSessionID()
	require.NoError(t, err)
	// RFC 9562: version is the top nibble of byte 6.
	version := id[uuidVersionByte] >> 4
	require.Equal(t, byte(7), version, "expected UUIDv7 version nibble")
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
