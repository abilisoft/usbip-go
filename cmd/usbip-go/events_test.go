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

// unknownEvent is a stand-in event whose dynamic type the
// classifier does NOT recognise; used to exercise the nil-return
// branch of classifyEvent.
type unknownEvent struct{}

func (unknownEvent) EventKind() domain.EventKind { return domain.EventKind(255) }

func mustParseAddrPort(t *testing.T, s string) netip.AddrPort {
	t.Helper()

	a, err := netip.ParseAddrPort(s)
	require.NoError(t, err)

	return a
}

// TestClassifyEventCoversEveryKind sweeps every concrete event the
// CLI is required to render, verifies classifyEvent returns a typed
// record and eventHeader extracts the expected kind + ISO-8601
// timestamp. Parametric so a new EventKind that lands in pkg/domain
// without an adapter here fails this test instead of silently
// rendering as fmt.Sprintf garbage.
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
	remote := domain.RemoteEndpoint{Host: "10.0.0.5", Port: 3240}

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
		{"device-bound", domain.DeviceBoundEvent{At: at, Device: dev}, "device_bound"},
		{"device-unbound", domain.DeviceUnboundEvent{At: at, Device: dev}, "device_unbound"},
		{
			name: "remote-device-added",
			ev:   domain.RemoteDeviceAddedEvent{At: at, Remote: remote, Device: dev},
			kind: "remote_device_added",
		},
		{
			name: "remote-device-removed",
			ev:   domain.RemoteDeviceRemovedEvent{At: at, Remote: remote, BusID: "1-1"},
			kind: "remote_device_removed",
		},
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

// TestClassifyEventReturnsNilOnUnknown pins the silent-failure path:
// an event whose dynamic type is not in the kind→adapter table must
// return nil so the JSON renderer can skip it without panicking.
func TestClassifyEventReturnsNilOnUnknown(t *testing.T) {
	t.Parallel()

	require.Nil(t, classifyEvent(unknownEvent{}))
}

// TestEventHeaderRejectsUnknownRecord pins the symmetric branch in
// eventHeader for record types it doesn't know about.
func TestEventHeaderRejectsUnknownRecord(t *testing.T) {
	t.Parallel()

	_, _, ok := eventHeader("a string is not an event record")
	require.False(t, ok)
}

// TestAdaptersRejectMismatchedDynamicType pins the type-assertion
// safety in every adapter — passing an event of the wrong concrete
// type returns nil, never panics.
func TestAdaptersRejectMismatchedDynamicType(t *testing.T) {
	t.Parallel()

	wrong := domain.PortAttachedEvent{At: time.Now()}

	require.Nil(t, adaptPortDetached(wrong))
	require.Nil(t, adaptPortErrored(wrong))
	require.Nil(t, adaptDeviceBound(wrong))
	require.Nil(t, adaptDeviceUnbound(wrong))
	require.Nil(t, adaptRemoteDeviceAdded(wrong))
	require.Nil(t, adaptRemoteDeviceRemoved(wrong))
	require.Nil(t, adaptSessionStarted(wrong))
	require.Nil(t, adaptSessionEnded(wrong))
}
