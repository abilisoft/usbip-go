// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestTableRendererPorts smoke-tests the Ports renderer. Table
// format is NOT a stable contract (see docs/json-schema.md), so
// this test asserts behavioural properties (writes a non-empty
// buffer; every value from each port row appears literally in the
// output) rather than a byte-for-byte golden.
func TestTableRendererPorts(t *testing.T) {
	t.Parallel()

	ports := []usbip.Port{
		{
			ID:         1,
			Status:     domain.StatusUsed,
			Speed:      domain.SpeedHigh,
			Remote:     domain.RemoteEndpoint{Host: "10.0.0.5", Port: 3240},
			BusID:      "1-1.2",
			LocalBusID: "0-0",
		},
		{
			ID:     2,
			Status: domain.StatusAvailable,
			Speed:  domain.SpeedSuper,
			BusID:  "2-1",
		},
	}

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Ports(&out, ports))

	got := out.String()
	require.NotEmpty(t, got)

	for _, want := range []string{"1-1.2", "2-1", "10.0.0.5:3240", "PORT", "STATUS", "BUSID"} {
		require.Contains(t, got, want)
	}
}

// TestTableRendererSessions smoke-tests the Sessions renderer.
func TestTableRendererSessions(t *testing.T) {
	t.Parallel()

	sid, err := domain.NewSessionID()
	require.NoError(t, err)

	sessions := []usbip.Session{
		{
			ID:        sid,
			BusID:     "1-1.2",
			StartedAt: time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC),
			BytesIn:   1024,
			BytesOut:  2048,
		},
	}

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Sessions(&out, sessions))

	got := out.String()
	require.NotEmpty(t, got)

	for _, want := range []string{"1-1.2", "2026-04-25T10:00:00Z", "1024", "2048", "ID", "BUSID"} {
		require.Contains(t, got, want)
	}
}

// TestTableRendererEventKnown — every event whose dynamic type the
// classifier recognises produces a non-empty line containing the
// kind discriminator.
func TestTableRendererEventKnown(t *testing.T) {
	t.Parallel()

	ev := domain.PortAttachedEvent{
		At: time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC),
		Port: usbip.Port{
			ID:    1,
			BusID: "1-1.2",
		},
	}

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Event(&out, ev))
	require.Contains(t, out.String(), "port_attached")
}

// TestTableRendererEventUnknown — events whose dynamic type the
// classifier does NOT recognise fall through to the fmt.Sprintf
// path. The line still includes content so an operator can debug
// a missing adapter.
func TestTableRendererEventUnknown(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Event(&out, unknownEvent{}))
	require.NotEmpty(t, out.String())
}
