package wire_test

import (
	"bytes"
	"encoding/binary"
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

	// Layout per wire/layout.go: device descriptor is 312 bytes with
	// Speed at offset 296 (after path[256] + busid[32] + busnum u32 +
	// devnum u32).
	const (
		offDevSpeed = 296
		deviceSize  = 312
	)

	buf := make([]byte, deviceSize)
	// busid "1-1" is always valid.
	copy(buf[256:288], []byte("1-1"))
	binary.BigEndian.PutUint32(buf[offDevSpeed:], 0xDEADBEEF)

	_, _, err := wire.DecodeDevice(bytes.NewReader(buf))
	require.Error(t, err,
		"decoder must reject a device whose Speed is outside the domain enum")
	require.ErrorIs(t, err, domain.ErrProtocolError,
		"speed-range rejection must wrap ErrProtocolError")
}
