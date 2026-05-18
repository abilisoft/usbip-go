// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/netip"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

type unknownEvent struct {
	kind domain.EventKind
}

func (e unknownEvent) EventKind() domain.EventKind { return e.kind }

func mustParseAddrPort(t *testing.T, s string) netip.AddrPort {
	t.Helper()

	a, err := netip.ParseAddrPort(s)
	require.NoError(t, err)

	return a
}

func TestClassifyEventCoversEveryKind(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 4, 25, 12, 34, 56, 0, time.UTC)
	wantAt := "2026-04-25T12:34:56Z"

	port := domain.Port{
		ID:     1,
		Status: domain.StatusUsed,
		Speed:  domain.SpeedHigh,
		Remote: domain.RemoteEndpoint{Host: "10.0.0.5", Port: 3240},
		BusID:  "1-1",
	}
	dev := domain.Device{BusID: "1-1", VendorID: 0x0951, ProductID: 0x1666}

	sid, err := domain.NewSessionID()
	require.NoError(t, err)

	sess := domain.Session{
		ID:         sid,
		RemoteAddr: mustParseAddrPort(t, "10.0.0.5:3240"),
		BusID:      "1-1",
		StartedAt:  at,
	}

	cases := []struct {
		name string
		ev   usbip.Event
		kind string
	}{
		{"port-attached", domain.PortAttachedEvent{At: at, Port: port}, "port_attached"},
		{"port-detached", domain.PortDetachedEvent{At: at, Port: port, Reason: "drop"}, "port_detached"},
		{"port-errored", domain.PortErroredEvent{At: at, Port: port, Err: "io"}, "port_errored"},
		{
			name: "port-reconnect-exhausted",
			ev: domain.PortReconnectExhaustedEvent{
				At:        at,
				Port:      port,
				Attempts:  3,
				LastError: "io: dial timeout",
			},
			kind: "port_reconnect_exhausted",
		},
		{"device-bound", domain.DeviceBoundEvent{At: at, Device: dev}, "device_bound"},
		{"device-unbound", domain.DeviceUnboundEvent{At: at, Device: dev}, "device_unbound"},
		{"session-started", domain.SessionStartedEvent{At: at, Session: sess}, "session_started"},
		{"session-ended", domain.SessionEndedEvent{At: at, Session: sess, Reason: "drain"}, "session_ended"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := classifyEvent(tc.ev)
			require.NotNil(t, rec, "classifyEvent must produce a record for %s", tc.name)

			gotKind, gotAt, ok := eventHeader(rec)
			require.True(t, ok, "eventHeader must classify %s", tc.name)
			require.Equal(t, tc.kind, gotKind)
			require.Equal(t, wantAt, gotAt)
		})
	}
}

func TestClassifyEventReturnsNilOnUnknown(t *testing.T) {
	t.Parallel()

	require.Nil(t, classifyEvent(unknownEvent{kind: domain.EventPortAttached}))
}

func TestEventHeaderRejectsUnknownRecord(t *testing.T) {
	t.Parallel()

	_, _, ok := eventHeader("a string is not an event record")
	require.False(t, ok)
}
