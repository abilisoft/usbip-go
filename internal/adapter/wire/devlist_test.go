package wire_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestEncodeOpReqDevlist produces exactly the 8-byte header.
func TestEncodeOpReqDevlist(t *testing.T) {
	t.Parallel()

	got := wire.EncodeOpReqDevlist()
	want := []byte{0x01, 0x11, 0x80, 0x05, 0, 0, 0, 0}
	require.Equal(t, want, got)
}

// TestDecodeOpRepDevlistZeroDevices is the spec §6.2 edge case:
// nDevices=0 is a valid response and returns (nil, nil).
func TestDecodeOpRepDevlistZeroDevices(t *testing.T) {
	t.Parallel()

	// Header + u32 count=0.
	buf := bytes.Buffer{}
	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 0})

	got, err := wire.DecodeOpRepDevlist(&buf)
	require.NoError(t, err)
	require.Nil(t, got)
}

// TestDecodeOpRepDevlistOneDeviceZeroInterfaces checks a single device
// with no interfaces.
func TestDecodeOpRepDevlistOneDeviceZeroInterfaces(t *testing.T) {
	t.Parallel()

	dev := domain.Device{
		Path:          "/sys/d",
		BusID:         domain.BusID("1-1"),
		BusNum:        1,
		DevNum:        2,
		Speed:         domain.SpeedHigh,
		VendorID:      0x1234,
		ProductID:     0x5678,
		BcdDevice:     0x0100,
		NumInterfaces: 0,
	}

	buf := bytes.Buffer{}
	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 1})
	require.NoError(t, wire.EncodeDevice(&buf, dev))

	got, err := wire.DecodeOpRepDevlist(&buf)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "/sys/d", got[0].Path)
	require.Equal(t, domain.BusID("1-1"), got[0].BusID)
	require.Empty(t, got[0].Interfaces)
}

// TestDecodeOpRepDevlistTwoDevicesWithInterfaces round-trips two
// devices each carrying two interface descriptors.
func TestDecodeOpRepDevlistTwoDevicesWithInterfaces(t *testing.T) {
	t.Parallel()

	mk := func(path, busid string, nintf uint8, intfs []domain.Interface) domain.Device {
		return domain.Device{
			Path:          path,
			BusID:         domain.BusID(busid),
			BusNum:        1,
			DevNum:        2,
			Speed:         domain.SpeedHigh,
			NumInterfaces: nintf,
			Interfaces:    intfs,
		}
	}

	d1 := mk("/a", "1-1", 2, []domain.Interface{
		{Class: 0x03, Subclass: 0x01, Protocol: 0x01},
		{Class: 0x03, Subclass: 0x01, Protocol: 0x02},
	})
	d2 := mk("/b", "1-2", 2, []domain.Interface{
		{Class: 0x08, Subclass: 0x06, Protocol: 0x50},
		{Class: 0x09, Subclass: 0x00, Protocol: 0x00},
	})

	var buf bytes.Buffer

	require.NoError(t, wire.EncodeOpRepDevlist(&buf, []domain.Device{d1, d2}))

	got, err := wire.DecodeOpRepDevlist(&buf)
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.Equal(t, d1.BusID, got[0].BusID)
	require.Len(t, got[0].Interfaces, 2)
	require.Equal(t, domain.USBClass(0x03), got[0].Interfaces[0].Class)
	require.Equal(t, uint8(0), got[0].Interfaces[0].Alt, "wire Alt must be zero")
	require.Equal(t, d2.BusID, got[1].BusID)
	require.Len(t, got[1].Interfaces, 2)
	require.Equal(t, domain.USBClass(0x08), got[1].Interfaces[0].Class)
	require.Equal(t, domain.USBProtocol(0x50), got[1].Interfaces[0].Protocol)
}

// TestDecodeOpRepDevlistTruncatedMidDevice: spec §6.2 — truncation
// mid-device returns io.ErrUnexpectedEOF wrapped with
// "truncated devlist at index N" where N is the successful-device count.
func TestDecodeOpRepDevlistTruncatedMidDevice(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 2})

	dev := domain.Device{
		Path:  "/a",
		BusID: domain.BusID("1-1"),
	}

	require.NoError(t, wire.EncodeDevice(&buf, dev))
	// second device truncated after half the descriptor.
	buf.Write(make([]byte, wire.DeviceWireSize/2))

	_, err := wire.DecodeOpRepDevlist(&buf)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.ErrorContains(t, err, "truncated devlist at index 1")
}

// TestDecodeOpRepDevlistTruncatedMidInterface: device header + partial
// interface bytes → io.ErrUnexpectedEOF with "truncated interfaces".
func TestDecodeOpRepDevlistTruncatedMidInterface(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 1})

	dev := domain.Device{
		Path:          "/a",
		BusID:         domain.BusID("1-1"),
		NumInterfaces: 2,
	}

	require.NoError(t, wire.EncodeDevice(&buf, dev))
	// Only 3 bytes of the first interface descriptor (4 bytes needed).
	buf.Write([]byte{0x01, 0x02, 0x03})

	_, err := wire.DecodeOpRepDevlist(&buf)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.ErrorContains(t, err, "truncated interfaces")
}

// TestDecodeOpRepDevlistInterfaceCountOverRemaining: when the declared
// bNumInterfaces would require more bytes than remain, same error.
func TestDecodeOpRepDevlistInterfaceCountOverRemaining(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 1})

	dev := domain.Device{
		Path:          "/a",
		BusID:         domain.BusID("1-1"),
		NumInterfaces: 50, // claim 50, supply only 4 bytes.
	}

	require.NoError(t, wire.EncodeDevice(&buf, dev))
	buf.Write(make([]byte, 4))

	_, err := wire.DecodeOpRepDevlist(&buf)
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
	require.ErrorContains(t, err, "truncated interfaces")
}

// TestDecodeOpRepDevlistTrailingBytes: trailing bytes beyond the
// declared device count are tolerated with a slog.Warn.
// Parallel-safe via slogDefaultMu in captureSlogAll.
func TestDecodeOpRepDevlistTrailingBytes(t *testing.T) {
	t.Parallel()

	captured := captureSlogAll(t)

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 0})
	// Garbage trailing bytes.
	buf.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF})

	got, err := wire.DecodeOpRepDevlist(&buf)
	require.NoError(t, err)
	require.Nil(t, got)

	msgs := captured()
	require.NotEmpty(t, msgs, "expected slog.Warn on trailing bytes")
	require.Contains(t, msgs[0], "trailing bytes after devlist")
}

// TestDecodeOpRepDevlistVersionMismatch: wrong version in header surfaces
// ErrProtocolMismatch.
func TestDecodeOpRepDevlistVersionMismatch(t *testing.T) {
	t.Parallel()

	// version=0x0112 instead of 0x0111.
	buf := []byte{0x01, 0x12, 0x00, 0x05, 0, 0, 0, 0, 0, 0, 0, 0}

	_, err := wire.DecodeOpRepDevlist(bytes.NewReader(buf))
	require.ErrorIs(t, err, domain.ErrProtocolMismatch)
}

// captureSlogAll captures all slog messages (any level). Uses
// slogDefaultMu (declared in main_test.go) to serialize with other
// slog-default-mutating tests so parallel execution is safe.
func captureSlogAll(t *testing.T) func() []string {
	t.Helper()

	slogDefaultMu.Lock()

	prev := slog.Default()
	h := &captureHandler{}
	slog.SetDefault(slog.New(h))

	t.Cleanup(func() {
		slog.SetDefault(prev)
		slogDefaultMu.Unlock()
	})

	return func() []string { return h.snapshot() }
}

// captureHandler records every log message. Concurrency-safe so tests
// that run in parallel while the slog default handler is swapped can
// share the handler without a data race on msgs.
type captureHandler struct {
	mu   sync.Mutex
	msgs []string
}

func (c *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (c *captureHandler) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, r.Message)
	c.mu.Unlock()

	return nil
}

func (c *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return c }

func (c *captureHandler) WithGroup(_ string) slog.Handler { return c }

func (c *captureHandler) snapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([]string, len(c.msgs))
	copy(out, c.msgs)

	return out
}
