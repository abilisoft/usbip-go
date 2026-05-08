package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// goldenDevices returns the deterministic device fixture used by both
// the table and JSON golden files.
func goldenDevices() []usbip.Device {
	return []usbip.Device{
		{
			BusID:     domain.BusID("1-1.2"),
			BusNum:    1,
			DevNum:    2,
			Speed:     domain.SpeedHigh,
			VendorID:  0x0951,
			ProductID: 0x1666,
			Class:     0x03,
		},
		{
			BusID:     domain.BusID("2-1"),
			BusNum:    2,
			DevNum:    3,
			Speed:     domain.SpeedSuper,
			VendorID:  0x1234,
			ProductID: 0x5678,
			Class:     0x00,
		},
	}
}

// TestTableRendererMatchesGolden — golden-file byte comparison for the
// table renderer.
func TestTableRendererMatchesGolden(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Devices(&out, goldenDevices()))

	golden := filepath.Join("testdata", "devices.txt")
	want, err := os.ReadFile(golden)
	require.NoError(t, err)
	require.Equal(t, string(want), out.String())
}

// TestJSONRendererMatchesGolden — structural JSON comparison for the
// JSON renderer. We re-parse both sides into maps so whitespace and
// field order don't contaminate the assertion.
func TestJSONRendererMatchesGolden(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, jsonRenderer{}.Devices(&out, goldenDevices()))

	golden := filepath.Join("testdata", "devices.json")
	want, err := os.ReadFile(golden)
	require.NoError(t, err)

	var got, expect map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	require.NoError(t, json.Unmarshal(want, &expect))
	require.Equal(t, expect, got)
}

// TestJSONRendererSchemaFirst — the top-level envelope has "schema"
// as the first key (spec §7.5 stability rule).
func TestJSONRendererSchemaFirst(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, jsonRenderer{}.Devices(&out, nil))
	require.Contains(t, out.String(), `"schema":"v1"`)
}

// TestJSONEventRecord — each concrete event emits a v1 record with the
// expected "kind" discriminator.
func TestJSONEventRecord(t *testing.T) {
	t.Parallel()

	ev := domain.PortAttachedEvent{Port: usbip.Port{ID: 1, BusID: "1-1.2"}}

	var out bytes.Buffer
	require.NoError(t, jsonRenderer{}.Event(&out, ev))

	var m map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &m))
	require.Equal(t, "v1", m["schema"])
	require.Equal(t, "port_attached", m["kind"])
}
