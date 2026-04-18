package wire_test

import (
	"bytes"
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
