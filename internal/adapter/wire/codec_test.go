package wire_test

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestNewCodecNotNil sanity-checks the constructor.
func TestNewCodecNotNil(t *testing.T) {
	t.Parallel()

	c := wire.NewCodec()
	require.NotNil(t, c)
}

// TestCodecEncodeDecodeOpReqImport round-trips a request through the
// Codec wrapper to prove the methods forward to the package-level
// helpers.
func TestCodecEncodeDecodeOpReqImport(t *testing.T) {
	t.Parallel()

	c := wire.NewCodec()

	var buf bytes.Buffer

	require.NoError(t, c.EncodeOpReqImport(&buf, domain.BusID("1-1")))

	got, err := c.DecodeOpReqImport(&buf)
	require.NoError(t, err)
	require.Equal(t, domain.BusID("1-1"), got)
}

// TestCodecEncodeDecodeOpRepDevlist round-trips a devlist reply through
// the Codec wrapper.
func TestCodecEncodeDecodeOpRepDevlist(t *testing.T) {
	t.Parallel()

	c := wire.NewCodec()

	dev := domain.Device{
		Path:  "/sys/x",
		BusID: domain.BusID("1-2"),
	}

	var buf bytes.Buffer

	require.NoError(t, c.EncodeOpRepDevlist(&buf, []domain.Device{dev}))

	got, err := c.DecodeOpRepDevlist(&buf)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, dev.BusID, got[0].BusID)
}

// TestCodecEncodeDecodeOpRepImport round-trips a success import reply
// through the Codec wrapper.
func TestCodecEncodeDecodeOpRepImport(t *testing.T) {
	t.Parallel()

	c := wire.NewCodec()

	dev := domain.Device{
		Path:  "/sys/y",
		BusID: domain.BusID("2-1"),
	}

	var buf bytes.Buffer

	require.NoError(t, c.EncodeOpRepImport(&buf, dev))

	got, err := c.DecodeOpRepImport(&buf)
	require.NoError(t, err)
	require.Equal(t, dev.BusID, got.BusID)
}

// TestCodecEncodeOpReqDevlist verifies the devlist request method.
func TestCodecEncodeOpReqDevlist(t *testing.T) {
	t.Parallel()

	c := wire.NewCodec()

	got := c.EncodeOpReqDevlist()
	require.Equal(t, wire.EncodeOpReqDevlist(), got)
}

// TestCodecDecodeOpRepDevlistLogsTrailingBytes verifies that
// Codec.DecodeOpRepDevlist, given a WithLogger injection, emits the
// "trailing bytes after payload" warn when the wire frame carries
// extra bytes past the declared count. The test uses a per-instance
// capture handler; no slog.Default() mutation, no shared state, fully
// parallel.
func TestCodecDecodeOpRepDevlistLogsTrailingBytes(t *testing.T) {
	t.Parallel()

	capture := newCaptureHandler()
	c := wire.NewCodec(wire.WithLogger(slog.New(capture)))

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 0})
	buf.Write([]byte{0xDE, 0xAD, 0xBE, 0xEF})

	got, err := c.DecodeOpRepDevlist(&buf)
	require.NoError(t, err)
	require.Nil(t, got)

	msgs := capture.snapshot()
	require.NotEmpty(t, msgs, "Codec must log trailing-bytes warn via injected logger")
	require.Contains(t, msgs[0], "trailing bytes after payload")
}

// TestCodecDecodeOpRepDevlistNilLogger checks that WithLogger(nil)
// selects a discard handler — trailing bytes don't blow up the codec
// and zero records reach any external sink.
func TestCodecDecodeOpRepDevlistNilLogger(t *testing.T) {
	t.Parallel()

	c := wire.NewCodec(wire.WithLogger(nil))

	var buf bytes.Buffer

	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))
	buf.Write([]byte{0, 0, 0, 0})
	buf.Write([]byte{0xDE, 0xAD})

	got, err := c.DecodeOpRepDevlist(&buf)
	require.NoError(t, err)
	require.Nil(t, got)
}

// captureHandler collects every slog.Record it handles. Instances are
// created per-test; concurrency-safe via an internal mutex so the
// handler can be safely shared within a single test's goroutines.
type captureHandler struct {
	mu   sync.Mutex
	msgs []string
}

func newCaptureHandler() *captureHandler { return &captureHandler{} }

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
