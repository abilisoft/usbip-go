package wire_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// encodeDeviceForFlagTest produces a valid 312-byte device descriptor
// seeded with "1-1" busid + path, ready for surgical mutation of the
// padded-string regions.
func encodeDeviceForFlagTest() []byte {
	var buf bytes.Buffer

	d := domain.Device{
		Path:          "/sys/devices/pci/usb",
		BusID:         domain.BusID("1-1"),
		BusNum:        1,
		DevNum:        1,
		Speed:         domain.SpeedHigh,
		VendorID:      0x0951,
		ProductID:     0x1666,
		BcdDevice:     0x0100,
		Class:         0,
		Subclass:      0,
		Protocol:      0,
		ConfigValue:   1,
		NumConfigs:    1,
		NumInterfaces: 0,
	}

	err := wire.EncodeDevice(&buf, d)
	if err != nil {
		panic(err)
	}

	return buf.Bytes()
}

// fillPaddedRegion overwrites [start, end) with printable ASCII so
// paddedStringFromBytes sees no NUL terminator and reports truncated.
func fillPaddedRegion(buf []byte, start, end int) {
	for i := start; i < end; i++ {
		buf[i] = 'x'
	}
}

// TestDecodeDeviceSurfacesTruncatedPaddedStringFlags pins the
// contract that DecodeDevice reports any padded-string field whose
// bytes never reach NUL. The §6.2 permissive-read rule keeps the
// decode non-erroring, but the flags struct carries the anomaly to
// the Codec layer where it becomes a slog.Warn.
func TestDecodeDeviceSurfacesTruncatedPaddedStringFlags(t *testing.T) {
	t.Parallel()

	raw := encodeDeviceForFlagTest()

	// Layout constants mirror wire/layout.go for test transparency.
	const (
		offDevPath  = 0
		offDevBusID = 256
		offDevEnd   = 288 // path[256] + busid[32]
	)

	fillPaddedRegion(raw, offDevPath, offDevBusID)

	dev, flags, err := wire.DecodeDevice(bytes.NewReader(raw))
	require.NoError(t, err)
	require.NotEmpty(t, dev.Path,
		"truncated path still decodes to a printable prefix per §6.2")
	require.Len(t, flags.TruncatedPaddedStrings, 1,
		"path truncation must be reported via DecodeFlags")
	require.Equal(t, "device.path", flags.TruncatedPaddedStrings[0].Field)

	// Mutate busid too and confirm both flags are surfaced independently.
	raw = encodeDeviceForFlagTest()
	fillPaddedRegion(raw, offDevBusID, offDevEnd)

	_, flags, err = wire.DecodeDevice(bytes.NewReader(raw))
	require.NoError(t, err)
	require.Len(t, flags.TruncatedPaddedStrings, 1)
	require.Equal(t, "device.busid", flags.TruncatedPaddedStrings[0].Field)
}

// TestCodecDecodeOpRepDevlistLogsTruncatedFields drives the Codec
// wrapper with an injected logger and asserts a Warn record fires for
// every truncated padded string in a devlist reply. device_index
// must match the 0-based offset inside the reply.
func TestCodecDecodeOpRepDevlistLogsTruncatedFields(t *testing.T) {
	t.Parallel()

	header := []byte{
		0x01, 0x11, // version 0x0111
		0, 0x05, // OP_REP_DEVLIST
		0, 0, 0, 0, // status
		0, 0, 0, 1, // count
	}

	devBuf := encodeDeviceForFlagTest()
	fillPaddedRegion(devBuf, 0, 256)

	var body bytes.Buffer

	body.Write(header)
	body.Write(devBuf)

	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, 0)
	body.Write(buf)

	sink := newSlogSink()
	c := wire.NewCodec(wire.WithLogger(slog.New(sink)))

	_, err := c.DecodeOpRepDevlist(&body)
	require.NoError(t, err)

	records := sink.records()
	require.NotEmpty(t, records, "truncated padded string must surface as a slog.Warn")

	var matched bool

	for _, r := range records {
		if r.hasAttr("field", "device.path") && r.hasAttr("device_index", int64(0)) {
			matched = true

			break
		}
	}

	require.True(t, matched,
		"Warn record must carry field=device.path and device_index=0")
}

// TestCodecDecodeOpRepImportLogsTruncatedFields mirrors the devlist
// assertion for the single-device OP_REP_IMPORT reply.
func TestCodecDecodeOpRepImportLogsTruncatedFields(t *testing.T) {
	t.Parallel()

	header := []byte{
		0x01, 0x11,
		0x00, 0x03, // OP_REP_IMPORT
		0, 0, 0, 0,
	}

	devBuf := encodeDeviceForFlagTest()
	fillPaddedRegion(devBuf, 256, 288)

	var body bytes.Buffer

	body.Write(header)
	body.Write(devBuf)

	sink := newSlogSink()
	c := wire.NewCodec(wire.WithLogger(slog.New(sink)))

	_, err := c.DecodeOpRepImport(&body)
	require.NoError(t, err)

	records := sink.records()

	var matched bool

	for _, r := range records {
		if r.hasAttr("field", "device.busid") {
			matched = true

			break
		}
	}

	require.True(t, matched, "Warn record must carry field=device.busid")
}

// slogSink is a minimal slog.Handler that captures Warn records for
// test introspection. Lives in this file so the test remains
// self-contained.
type slogSink struct {
	rs []slogRecord
}

type slogRecord struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

func (r slogRecord) hasAttr(key string, want any) bool {
	v, ok := r.attrs[key]

	return ok && v == want
}

func newSlogSink() *slogSink {
	return &slogSink{}
}

func (*slogSink) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (s *slogSink) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any, r.NumAttrs())

	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()

		return true
	})

	s.rs = append(s.rs, slogRecord{level: r.Level, msg: r.Message, attrs: attrs})

	return nil
}

func (s *slogSink) WithAttrs(_ []slog.Attr) slog.Handler { return s }
func (s *slogSink) WithGroup(_ string) slog.Handler      { return s }

func (s *slogSink) records() []slogRecord {
	out := make([]slogRecord, len(s.rs))
	copy(out, s.rs)

	return out
}

