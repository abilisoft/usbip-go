package wire_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// deviceFixture mirrors the JSON sidecar format for a device fixture.
// Byte-for-byte round-trip: decode(*.bin) must equal the values in the
// *.json sidecar; re-encode(decoded) must equal the original *.bin.
type deviceFixture struct {
	Path          string
	BusID         string
	BusNum        uint16
	DevNum        uint16
	Speed         domain.Speed
	VendorID      uint16
	ProductID     uint16
	BcdDevice     uint16
	Class         domain.USBClass
	Subclass      domain.USBSubclass
	Protocol      domain.USBProtocol
	ConfigValue   uint8
	NumConfigs    uint8
	NumInterfaces uint8
}

// TestDecodeDeviceHSKingstonFixture decodes the committed HS fixture and
// compares each field against the JSON expectation.
func TestDecodeDeviceHSKingstonFixture(t *testing.T) {
	t.Parallel()

	assertDeviceFixture(t, "device_hs_kingston")
}

// TestDecodeDeviceSSFixture checks the SuperSpeed variant.
func TestDecodeDeviceSSFixture(t *testing.T) {
	t.Parallel()

	assertDeviceFixture(t, "device_ss_sample")
}

// assertDeviceFixture loads a device_*.bin + device_*.json pair and
// validates decode + re-encode byte-for-byte.
func assertDeviceFixture(t *testing.T, name string) {
	t.Helper()

	binBytes, err := os.ReadFile(filepath.Join("testdata", name+".bin"))
	require.NoError(t, err)
	require.Len(t, binBytes, wire.DeviceWireSize)

	jsonBytes, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	require.NoError(t, err)

	var want deviceFixture

	require.NoError(t, json.Unmarshal(jsonBytes, &want))

	got, err := wire.DecodeDevice(bytes.NewReader(binBytes))
	require.NoError(t, err)
	require.Equal(t, want.Path, got.Path)
	require.Equal(t, domain.BusID(want.BusID), got.BusID)
	require.Equal(t, want.BusNum, got.BusNum)
	require.Equal(t, want.DevNum, got.DevNum)
	require.Equal(t, want.Speed, got.Speed)
	require.Equal(t, want.VendorID, got.VendorID)
	require.Equal(t, want.ProductID, got.ProductID)
	require.Equal(t, want.BcdDevice, got.BcdDevice)
	require.Equal(t, want.Class, got.Class)
	require.Equal(t, want.Subclass, got.Subclass)
	require.Equal(t, want.Protocol, got.Protocol)
	require.Equal(t, want.ConfigValue, got.ConfigValue)
	require.Equal(t, want.NumConfigs, got.NumConfigs)
	require.Equal(t, want.NumInterfaces, got.NumInterfaces)

	// Re-encode and compare byte-for-byte.
	var encoded bytes.Buffer

	require.NoError(t, wire.EncodeDevice(&encoded, got))
	require.Equal(t, binBytes, encoded.Bytes())
}

// TestDecodeDeviceShortRead fails with a wrapped io.ErrUnexpectedEOF.
func TestDecodeDeviceShortRead(t *testing.T) {
	t.Parallel()

	_, err := wire.DecodeDevice(bytes.NewReader(make([]byte, wire.DeviceWireSize-1)))
	require.Error(t, err)
}

// TestEncodeDeviceBusIDOverflow rejects a busid longer than BusIDSize-1.
func TestEncodeDeviceBusIDOverflow(t *testing.T) {
	t.Parallel()

	d := domain.Device{
		BusID: domain.BusID(bytes.Repeat([]byte{'a'}, domain.BusIDSize)),
	}

	var buf bytes.Buffer

	err := wire.EncodeDevice(&buf, d)
	require.ErrorIs(t, err, domain.ErrBusIDInvalid)
}

// TestEncodeDevicePathOverflow rejects a path longer than SysPathSize-1.
func TestEncodeDevicePathOverflow(t *testing.T) {
	t.Parallel()

	d := domain.Device{
		Path: string(bytes.Repeat([]byte{'a'}, domain.SysPathSize)),
	}

	var buf bytes.Buffer

	err := wire.EncodeDevice(&buf, d)
	require.ErrorIs(t, err, domain.ErrProtocolError)
}
