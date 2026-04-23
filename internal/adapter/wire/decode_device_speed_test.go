package wire_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestDecodeDeviceRejectsUnknownSpeed pins the invariant that the
// wire decoder rejects a Speed field outside the domain's finite
// enum. A peer emitting 0xDEADBEEF today round-trips as a
// domain.Speed(0xDEADBEEF); downstream consumers (metrics cardinality,
// CLI rendering, event delivery) have no way to notice the garbage.
// Rejecting at decode time forces the protocol mismatch to surface
// where it can be classified, not after the value has been logged
// or passed into an iter.Seq.
func TestDecodeDeviceRejectsUnknownSpeed(t *testing.T) {
	t.Parallel()

	// 312-byte well-formed device descriptor with Speed set to an
	// out-of-range value. Layout constants live in wire/layout.go;
	// duplicated here because they're package-private.
	const (
		offDevSpeed = 268
		deviceSize  = 312
	)

	buf := make([]byte, deviceSize)
	// busid "1-1" to avoid unrelated rejections from future wire
	// hardening.
	copy(buf[256:288], []byte("1-1"))
	binary.BigEndian.PutUint32(buf[offDevSpeed:], 0xDEADBEEF)

	_, err := wire.DecodeDevice(bytes.NewReader(buf))
	require.Error(t, err,
		"decoder must reject a device whose Speed is outside the domain enum")
	require.True(t, errors.Is(err, domain.ErrProtocolError),
		"speed-range rejection must wrap ErrProtocolError")
}
