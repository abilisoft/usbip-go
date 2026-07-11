// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestRenderDetachResult_Table pins the styled human-readable
// detach acknowledgement: "✓ detached port <id>" composed via
// formatAck, written through styleWriter. Without a dedicated
// test, a regression that drops the pretty path (or mixes JSON
// output into table mode) would only surface in a manual run.
func TestRenderDetachResult_Table(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, renderDetachResult(&out, outputTable, usbip.PortID(7)))

	got := out.String()
	require.Contains(t, got, "detached port",
		"table-mode ack must contain the action verb")
	require.Contains(t, got, "7",
		"table-mode ack must include the port id")
	require.True(t, strings.Contains(got, "\x1b[") || strings.Contains(got, "✓"),
		"table-mode ack must run through the styled formatter (ANSI escapes or check-mark glyph); got: %q", got)
}

// TestRenderDetachResult_JSON pins the schema-stable JSON path:
// {"schema":"v1","op":testDetachCommand,"ok":true,"port_id":<id>}.
// The JSON contract is the subprocess-facing surface; the styled
// table is operator-only.
func TestRenderDetachResult_JSON(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, renderDetachResult(&out, outputJSON, usbip.PortID(42)))

	var got map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &got),
		"JSON-mode ack must emit valid JSON; got: %s", out.String())

	require.Equal(t, "v1", got["schema"])
	require.Equal(t, testDetachCommand, got["op"])
	require.Equal(t, true, got["ok"])
	// json.Unmarshal decodes JSON numbers as float64, so the
	// comparison must use a float-aware assertion. The port-id is an
	// integer round-trip; InDelta with epsilon 0 enforces exact
	// equality without testifylint flagging Equal-on-float.
	gotPort, ok := got["port_id"].(float64)
	require.True(t, ok, "port_id must decode as JSON number; got %T", got["port_id"])
	require.InDelta(t, float64(42), gotPort, 0)
}
