// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package wire_test

import (
	"bytes"
	"embed"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/*.json
var deviceFixtureFS embed.FS

// Synthetic device-descriptor fixtures (spec §6.2 byte layout). Inlined
// as hex so the repo never carries binary blobs; mustDecodeHex strips
// whitespace so the strings can be reformatted freely.
const (
	deviceHSKingstonHex = `
		2f7379732f646576696365732f706369303030303a30302f303030303a30303a
		31342e302f757362312f312d3100000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		312d310000000000000000000000000000000000000000000000000000000000
		000000010000000200000003095116660110000000010101
	`

	deviceSSSampleHex = `
		2f7379732f646576696365732f706369303030303a30302f303030303a30303a
		31342e302f757362322f322d3100000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		0000000000000000000000000000000000000000000000000000000000000000
		322d310000000000000000000000000000000000000000000000000000000000
		000000020000000300000005095116660110000000010101
	`
)

func syntheticDeviceBytes(t *testing.T, name string) []byte {
	t.Helper()

	var hx string

	switch name {
	case "device_hs_kingston":
		hx = deviceHSKingstonHex
	case "device_ss_sample":
		hx = deviceSSSampleHex
	default:
		t.Fatalf("unknown synthetic device fixture %q", name)
	}

	b, err := hex.DecodeString(strings.Join(strings.Fields(hx), ""))
	require.NoError(t, err)

	return b
}

// deviceFixture mirrors the JSON sidecar format for a device fixture.
// Byte-for-byte round-trip: decode(hex-inlined bytes) must equal the
// values in the *.json sidecar; re-encode(decoded) must equal the
// original bytes.
type deviceFixture struct {
	Path          string             `json:"path"`
	BusID         string             `json:"busid"`
	BusNum        uint16             `json:"busnum"`
	DevNum        uint16             `json:"devnum"`
	Speed         domain.Speed       `json:"speed"`
	VendorID      uint16             `json:"vendor_id"`
	ProductID     uint16             `json:"product_id"`
	BcdDevice     uint16             `json:"bcd_device"`
	Class         domain.USBClass    `json:"class"`
	Subclass      domain.USBSubclass `json:"subclass"`
	Protocol      domain.USBProtocol `json:"protocol"`
	ConfigValue   uint8              `json:"config_value"`
	NumConfigs    uint8              `json:"num_configs"`
	NumInterfaces uint8              `json:"num_interfaces"`
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

// assertDeviceFixture loads a synthetic device fixture (inline hex) +
// its JSON sidecar and validates decode + re-encode byte-for-byte.
func assertDeviceFixture(t *testing.T, name string) {
	t.Helper()

	binBytes := syntheticDeviceBytes(t, name)
	require.Len(t, binBytes, wire.DeviceWireSize)

	jsonBytes, err := deviceFixtureFS.ReadFile("testdata/" + name + ".json")
	require.NoError(t, err)

	var want deviceFixture

	require.NoError(t, json.Unmarshal(jsonBytes, &want))

	got, _, err := wire.DecodeDevice(bytes.NewReader(binBytes))
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

	_, _, err := wire.DecodeDevice(bytes.NewReader(make([]byte, wire.DeviceWireSize-1)))
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
