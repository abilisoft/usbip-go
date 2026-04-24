package wire_test

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestDecodeDeviceRejectsZeroNumConfigs pins the USB-layer invariant
// that a device always advertises at least one configuration
// (bNumConfigurations >= 1 per USB 2.0 §9.6.1). A peer reporting 0
// is either a buggy emulator or a hostile crafter; a decoded device
// whose NumConfigs is 0 then slips through ConfigValue bookkeeping
// unchecked and confuses every downstream consumer that uses it as
// a loop bound.
func TestDecodeDeviceRejectsZeroNumConfigs(t *testing.T) {
	t.Parallel()

	buf := encodeValidDevice(t)
	const offNumConfigs = 310
	buf[offNumConfigs] = 0

	_, _, err := wire.DecodeDevice(bytes.NewReader(buf))
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrProtocolError,
		"NumConfigs=0 must surface as ErrProtocolError")
}

// TestDecodeDeviceRejectsConfigValueAboveNumConfigs pins the
// invariant that the active ConfigValue cannot exceed NumConfigs —
// the kernel would refuse such a device at enumeration and the peer
// emitting it is telling us something impossible. Surface the
// mismatch so downstream consumers don't trust a bogus "active
// config 5 of 1" entry.
func TestDecodeDeviceRejectsConfigValueAboveNumConfigs(t *testing.T) {
	t.Parallel()

	buf := encodeValidDevice(t)
	const (
		offConfigValue = 309
		offNumConfigs  = 310
	)

	buf[offNumConfigs] = 1
	buf[offConfigValue] = 5

	_, _, err := wire.DecodeDevice(bytes.NewReader(buf))
	require.Error(t, err)
	require.ErrorIs(t, err, domain.ErrProtocolError,
		"ConfigValue > NumConfigs must surface as ErrProtocolError")
}

// encodeValidDevice builds a device whose u16 narrowing passes and
// whose ConfigValue / NumConfigs / Speed are all valid. Tests mutate
// the specific byte offset they care about and leave everything
// else well-formed so the rejection branch is pinned to the
// specific invariant under test.
func encodeValidDevice(t *testing.T) []byte {
	t.Helper()

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
	require.NoError(t, err)

	// Narrow a copy so callers can mutate without affecting future
	// helper outputs.
	out := make([]byte, buf.Len())
	copy(out, buf.Bytes())

	// Safety: the Speed field lives at offset 296; keep it populated
	// with SpeedHigh so decode passes the IsKnown gate.
	binary.BigEndian.PutUint32(out[296:], uint32(domain.SpeedHigh))

	return out
}
