// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// schemaFirstPrefix is the literal byte prefix every v1 top-level
// JSON record must start with. Locking it as a constant avoids
// magic-string drift across the schema-first assertions.
const schemaFirstPrefix = `{"schema":`

// goldenDevices returns the deterministic device fixture used by both
// the table and JSON golden files.
func goldenDevices() []usbip.Device {
	return []usbip.Device{
		{
			BusID:         domain.BusID("1-1.2"),
			BusNum:        1,
			DevNum:        2,
			Speed:         domain.SpeedHigh,
			VendorID:      0x0951,
			ProductID:     0x1666,
			Class:         0x03,
			NumInterfaces: 1,
			Manufacturer:  "Kingston",
			Product:       "DataTraveler",
		},
		{
			BusID:         domain.BusID("2-1"),
			BusNum:        2,
			DevNum:        3,
			Speed:         domain.SpeedSuper,
			VendorID:      0x1234,
			ProductID:     0x5678,
			Class:         0x00,
			NumInterfaces: 3,
		},
	}
}

// TestTableRendererMatchesGolden — golden-file byte comparison for the
// table renderer.
func TestTableRendererMatchesGolden(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Devices(&out, goldenDevices()))

	want, err := os.ReadFile("testdata/devices.txt")
	require.NoError(t, err)
	require.Equal(t, string(want), out.String())
}

// TestJSONRendererMatchesGolden — byte-exact golden-file comparison for
// the JSON renderer. The byte-level assertion is load-bearing: v1 contract §7.5
// requires "schema" to be the first field, and only byte-equal testing
// catches regressions in Go json.Marshal's map-key sort order.
func TestJSONRendererMatchesGolden(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, jsonRenderer{}.Devices(&out, goldenDevices()))

	want, err := os.ReadFile("testdata/devices.json")
	require.NoError(t, err)
	require.Equal(t, string(want), out.String())
}

// TestJSONRendererDevicesSchemaFirst — Devices emits a top-level record
// that starts with `{"schema":` byte-for-byte (v1 contract §7.5 stability rule).
func TestJSONRendererDevicesSchemaFirst(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, jsonRenderer{}.Devices(&out, goldenDevices()))
	require.True(
		t,
		bytes.HasPrefix(out.Bytes(), []byte(schemaFirstPrefix)),
		"Devices output must begin with %q, got %s", schemaFirstPrefix, out.String(),
	)
}

// TestJSONRendererPortsSchemaFirst — Ports emits a top-level record
// whose first key is "schema".
func TestJSONRendererPortsSchemaFirst(t *testing.T) {
	t.Parallel()

	ports := []usbip.Port{
		{ID: 1, Status: domain.StatusUsed, Speed: domain.SpeedHigh, BusID: "1-1.2"},
	}

	var out bytes.Buffer
	require.NoError(t, jsonRenderer{}.Ports(&out, ports))
	require.True(
		t,
		bytes.HasPrefix(out.Bytes(), []byte(schemaFirstPrefix)),
		"Ports output must begin with %q, got %s", schemaFirstPrefix, out.String(),
	)
}

// TestJSONRendererSessionsSchemaFirst — Sessions emits a top-level record
// whose first key is "schema".
func TestJSONRendererSessionsSchemaFirst(t *testing.T) {
	t.Parallel()

	sessions := []usbip.Session{
		{
			ID:        domain.SessionID{},
			BusID:     "1-1.2",
			StartedAt: time.Unix(0, 0).UTC(),
		},
	}

	var out bytes.Buffer
	require.NoError(t, jsonRenderer{}.Sessions(&out, sessions))
	require.True(
		t,
		bytes.HasPrefix(out.Bytes(), []byte(schemaFirstPrefix)),
		"Sessions output must begin with %q, got %s", schemaFirstPrefix, out.String(),
	)
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
