package wire

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestPaddedStringRoundTripBusID verifies that writePaddedString produces
// exactly BusIDSize bytes and that readPaddedString recovers the string.
func TestPaddedStringRoundTripBusID(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writePaddedString(&buf, "1-1", domain.BusIDSize)
	require.NoError(t, err)
	require.Equal(t, domain.BusIDSize, buf.Len())

	got, err := readPaddedString(&buf, domain.BusIDSize)
	require.NoError(t, err)
	require.Equal(t, "1-1", got)
}

// TestPaddedStringRoundTripPath verifies path (256-byte) round-trip.
func TestPaddedStringRoundTripPath(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writePaddedString(&buf, "/sys/devices/pci0000:00/usb1/1-1", domain.SysPathSize)
	require.NoError(t, err)
	require.Equal(t, domain.SysPathSize, buf.Len())

	got, err := readPaddedString(&buf, domain.SysPathSize)
	require.NoError(t, err)
	require.Equal(t, "/sys/devices/pci0000:00/usb1/1-1", got)
}

// TestWritePaddedStringBusIDOverflow: writing a string of length >=
// BusIDSize into a BusIDSize-wide field yields ErrBusIDInvalid (no room
// for the required trailing NUL byte).
func TestWritePaddedStringBusIDOverflow(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	// Length exactly 32 — no room for trailing NUL.
	err := writePaddedString(&buf, strings.Repeat("a", domain.BusIDSize), domain.BusIDSize)
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
}

// TestWritePaddedStringPathOverflow: oversized path → ErrProtocolError.
func TestWritePaddedStringPathOverflow(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	err := writePaddedString(&buf, strings.Repeat("a", domain.SysPathSize), domain.SysPathSize)
	require.ErrorIs(t, err, domain.ErrProtocolError)
}

// TestReadPaddedStringShortRead: reader returns fewer bytes than size →
// io.ErrUnexpectedEOF.
func TestReadPaddedStringShortRead(t *testing.T) {
	t.Parallel()

	partial := make([]byte, domain.BusIDSize-1)

	_, err := readPaddedString(bytes.NewReader(partial), domain.BusIDSize)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

// TestReadPaddedStringNonNULTerminated: a full-size buffer with no NUL
// byte is truncated at the buffer boundary; no error; slog.Warn emitted.
func TestReadPaddedStringNonNULTerminated(t *testing.T) {
	t.Parallel()

	restoreWarn := captureSlogWarn(t)

	buf := bytes.Repeat([]byte{'A'}, domain.BusIDSize)

	got, err := readPaddedString(bytes.NewReader(buf), domain.BusIDSize)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("A", domain.BusIDSize), got)

	warns := restoreWarn()
	require.NotEmpty(t, warns, "expected slog.Warn on non-NUL-terminated buffer")
	require.Contains(t, warns[0], "non-NUL-terminated padded string")
}

// TestReadPaddedStringMidBufferNUL: a mid-buffer NUL truncates the
// returned string at the first NUL; no warn emitted.
func TestReadPaddedStringMidBufferNUL(t *testing.T) {
	t.Parallel()

	restoreWarn := captureSlogWarn(t)

	buf := make([]byte, domain.BusIDSize)
	copy(buf, []byte("1-1\x00junk-after-nul"))

	got, err := readPaddedString(bytes.NewReader(buf), domain.BusIDSize)
	require.NoError(t, err)
	require.Equal(t, "1-1", got)
	require.Empty(t, restoreWarn(), "no warn expected when NUL is present")
}

// captureSlogWarn replaces the default slog logger with a handler that
// records Warn messages. It returns a function that restores the prior
// default and returns captured warning messages.
func captureSlogWarn(t *testing.T) func() []string {
	t.Helper()

	prev := slog.Default()
	captured := &warnCapture{}
	slog.SetDefault(slog.New(captured))

	t.Cleanup(func() { slog.SetDefault(prev) })

	return func() []string {
		return captured.snapshot()
	}
}

// warnCapture is a minimal slog.Handler that records Warn-level messages.
type warnCapture struct {
	msgs []string
}

func (w *warnCapture) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (w *warnCapture) Handle(_ context.Context, r slog.Record) error {
	if r.Level == slog.LevelWarn {
		w.msgs = append(w.msgs, r.Message)
	}

	return nil
}

func (w *warnCapture) WithAttrs(_ []slog.Attr) slog.Handler { return w }

func (w *warnCapture) WithGroup(_ string) slog.Handler { return w }

func (w *warnCapture) snapshot() []string {
	out := make([]string, len(w.msgs))
	copy(out, w.msgs)

	return out
}
