package app_test

import (
	"bytes"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestProtocolCodecSatisfiedByWireCodec drives a *wire.Codec through the
// app.ProtocolCodec interface so the compile-time assertion in
// interfaces.go is covered by at least one live exercise of every
// method shape. Any future drift between the interface and the wire
// package's method signatures surfaces here as a build failure.
func TestProtocolCodecSatisfiedByWireCodec(t *testing.T) {
	t.Parallel()

	var codec app.ProtocolCodec = wire.NewCodec()

	require.NotEmpty(t, codec.EncodeOpReqDevlist())

	var buf bytes.Buffer

	require.NoError(t, codec.EncodeOpReqImport(&buf, domain.BusID("1-1")))

	busID, err := codec.DecodeOpReqImport(&buf)
	require.NoError(t, err)
	require.Equal(t, domain.BusID("1-1"), busID)

	buf.Reset()
	require.NoError(t, codec.EncodeOpRepDevlist(&buf, nil))

	devs, err := codec.DecodeOpRepDevlist(&buf)
	require.NoError(t, err)
	require.Empty(t, devs)

	buf.Reset()
	dev := domain.Device{Path: "/sys/d", BusID: domain.BusID("2-1")}
	require.NoError(t, codec.EncodeOpRepImport(&buf, dev))

	decoded, err := codec.DecodeOpRepImport(&buf)
	require.NoError(t, err)
	require.Equal(t, dev.BusID, decoded.BusID)

	buf.Reset()
	buf.Write(wire.EncodeHeader(wire.OpRepDevlist, 0))

	version, op, status, err := codec.DecodeHeader(&buf)
	require.NoError(t, err)
	require.Equal(t, wire.OpRepDevlist, op)
	require.Equal(t, uint32(0), status)
	require.NotZero(t, version)
}
