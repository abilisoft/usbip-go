package wire_test

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestPaddedStringRoundTripBusID verifies that wire.WritePaddedString produces
// exactly BusIDSize bytes and that wire.ReadPaddedString recovers the string.
func TestPaddedStringRoundTripBusID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := wire.WritePaddedString(&buf, "1-1", domain.BusIDSize)
	require.NoError(t, err)
	require.Equal(t, domain.BusIDSize, buf.Len())

	got, truncated, err := wire.ReadPaddedString(&buf, domain.BusIDSize)
	require.NoError(t, err)
	require.Equal(t, "1-1", got)
	require.False(t, truncated, "NUL-terminated frame should not report truncation")
}

// TestPaddedStringRoundTripPath verifies path (256-byte) round-trip.
func TestPaddedStringRoundTripPath(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := wire.WritePaddedString(&buf, "/sys/devices/pci0000:00/usb1/1-1", domain.SysPathSize)
	require.NoError(t, err)
	require.Equal(t, domain.SysPathSize, buf.Len())

	got, truncated, err := wire.ReadPaddedString(&buf, domain.SysPathSize)
	require.NoError(t, err)
	require.Equal(t, "/sys/devices/pci0000:00/usb1/1-1", got)
	require.False(t, truncated)
}

// TestWritePaddedStringBusIDOverflow: writing a string of length >=
// BusIDSize into a BusIDSize-wide field yields ErrBusIDInvalid (no room
// for the required trailing NUL byte).
func TestWritePaddedStringBusIDOverflow(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	// Length exactly 32 — no room for trailing NUL.
	err := wire.WritePaddedString(&buf, strings.Repeat("a", domain.BusIDSize), domain.BusIDSize)
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
}

// TestWritePaddedStringPathOverflow: oversized path → ErrProtocolError.
func TestWritePaddedStringPathOverflow(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := wire.WritePaddedString(&buf, strings.Repeat("a", domain.SysPathSize), domain.SysPathSize)
	require.ErrorIs(t, err, domain.ErrProtocolError)
}

// TestReadPaddedStringShortRead: reader returns fewer bytes than size →
// io.ErrUnexpectedEOF.
func TestReadPaddedStringShortRead(t *testing.T) {
	t.Parallel()

	partial := make([]byte, domain.BusIDSize-1)

	_, _, err := wire.ReadPaddedString(bytes.NewReader(partial), domain.BusIDSize)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

// TestReadPaddedStringNonNULTerminated: a full-size buffer with no NUL
// byte is truncated at the buffer boundary; no error; the truncated
// flag is set.
//
// No shared global state, fully parallelisable.
func TestReadPaddedStringNonNULTerminated(t *testing.T) {
	t.Parallel()

	buf := bytes.Repeat([]byte{'A'}, domain.BusIDSize)

	got, truncated, err := wire.ReadPaddedString(bytes.NewReader(buf), domain.BusIDSize)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("A", domain.BusIDSize), got)
	require.True(t, truncated, "non-NUL-terminated buffer must set truncated flag")
}

// TestReadPaddedStringMidBufferNUL: a mid-buffer NUL truncates the
// returned string at the first NUL; truncated flag is NOT set.
func TestReadPaddedStringMidBufferNUL(t *testing.T) {
	t.Parallel()

	buf := make([]byte, domain.BusIDSize)
	copy(buf, []byte("1-1\x00junk-after-nul"))

	got, truncated, err := wire.ReadPaddedString(bytes.NewReader(buf), domain.BusIDSize)
	require.NoError(t, err)
	require.Equal(t, "1-1", got)
	require.False(t, truncated, "NUL-terminated frame should not set truncated flag")
}
