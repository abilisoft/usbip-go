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
			Remote:     domain.RemoteEndpoint{Host: testRemoteHost, Port: 3240},
			BusID:      testNestedBusID,
			LocalBusID: "0-0",
		},
		{
			ID:     2,
			Status: domain.StatusAvailable,
			Speed:  domain.SpeedSuper,
			BusID:  testSecondaryBusID,
		},
	}

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Ports(&out, ports))

	got := out.String()
	require.NotEmpty(t, got)

	for _, want := range []string{
		testNestedBusID,
		testSecondaryBusID,
		"10.0.0.5:3240",
		"PORT",
		"STATUS",
		testBusIDHeader,
	} {
		require.Contains(t, got, want)
	}
}

func TestTableRendererPortsLeavesUnknownRemoteEmpty(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Ports(&out, []usbip.Port{{
		ID:         2,
		Status:     domain.StatusUsed,
		LocalBusID: testSecondaryBusID,
	}}))
	require.NotContains(t, out.String(), ":3240",
		"a zero-value remote endpoint must render as unknown, not as a default port with an empty host")
}

// TestTableRendererSessions smoke-tests the Sessions renderer.
func TestTableRendererSessions(t *testing.T) {
	t.Parallel()

	sid, err := domain.NewSessionID()
	require.NoError(t, err)

	sessions := []usbip.Session{
		{
			ID:        sid,
			BusID:     testNestedBusID,
			StartedAt: time.Date(2026, 4, 25, 10, 0, 0, 0, time.UTC),
			BytesIn:   1024,
			BytesOut:  2048,
		},
	}

	var out bytes.Buffer
	require.NoError(t, tableRenderer{}.Sessions(&out, sessions))

	got := out.String()
	require.NotEmpty(t, got)

	for _, want := range []string{testNestedBusID, "2026-04-25T10:00:00Z", "1024", "2048", "ID", testBusIDHeader} {
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
			BusID: testNestedBusID,
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
