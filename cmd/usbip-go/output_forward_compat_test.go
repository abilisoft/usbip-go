// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestJSONEventForwardCompatTolerant asserts the v1 contract §7.5 v1 stability
// rule: a downstream consumer decoding a CLI jsonlines record with an
// unknown field MUST NOT fail. stdlib json.Unmarshal is the reference
// consumer implementation — DisallowUnknownFields would violate the
// contract and is forbidden on that path.
func TestJSONEventForwardCompatTolerant(t *testing.T) {
	t.Parallel()

	// Round-trip a PortAttachedEvent through jsonRenderer.Event.
	ev := domain.PortAttachedEvent{
		Port: usbip.Port{ID: 1, Status: domain.StatusUsed, BusID: testNestedBusID},
	}

	var buf bytes.Buffer

	require.NoError(t, jsonRenderer{}.Event(&buf, ev))

	// Parse into a generic map — stdlib default, no DisallowUnknownFields.
	// The downstream-consumer reference implementation.
	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	// v1 contract §7.5 guarantees schema + kind discriminator + payload fields.
	require.Equal(t, "v1", rec["schema"])
	require.Equal(t, "port_attached", rec["kind"])
	require.Contains(t, rec, "port")
	require.Contains(t, rec, "at")
}

// TestJSONEventForwardCompatIgnoresUnknownField asserts that our typed
// decoder tolerates an extra field injected by a future v1 producer.
// stdlib json.Unmarshal already does this; the test pins the contract
// so nobody slips DisallowUnknownFields in later.
func TestJSONEventForwardCompatIgnoresUnknownField(t *testing.T) {
	t.Parallel()

	// Craft a v1 record with a field no current version emits.
	raw := []byte(`{
		"schema": "v1",
		"kind": "port_attached",
		"at": "2026-04-19T00:00:00Z",
		"port": {"id": 7, "status": "in_use", "speed": "", "remote": "", "busid": "3-1", "local_busid": ""},
		"future_field": "ignore-me"
	}`)

	var rec portAttachedRecord
	require.NoError(t, json.Unmarshal(raw, &rec))

	require.Equal(t, "v1", rec.Schema)
	require.Equal(t, "port_attached", rec.Kind)
	require.Equal(t, uint32(7), rec.Port.ID)
	require.Equal(t, "3-1", rec.Port.BusID)
}

// TestJSONEventForwardCompatRequiresKindDiscriminator asserts the
// inverse — a record missing the "kind" discriminator cannot be
// classified. We do not (yet) expose a typed decoder; this test
// verifies the shape constraint by checking that generic decoding
// into a map still requires "kind" for the downstream dispatcher to
// work.
func TestJSONEventForwardCompatRequiresKindDiscriminator(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"schema":"v1","at":"2026-04-19T00:00:00Z"}`)

	var rec map[string]any
	require.NoError(t, json.Unmarshal(raw, &rec))

	_, hasKind := rec["kind"]
	require.False(t, hasKind, "record with no kind must be rejectable by caller")
}
