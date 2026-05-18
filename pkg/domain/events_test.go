// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestEventKind_DistinctValues(t *testing.T) {
	t.Parallel()

	kinds := []domain.EventKind{
		domain.EventPortAttached,
		domain.EventPortDetached,
		domain.EventPortErrored,
		domain.EventPortReconnectExhausted,
		domain.EventDeviceBound,
		domain.EventDeviceUnbound,
		domain.EventSessionStarted,
		domain.EventSessionEnded,
	}

	seen := make(map[domain.EventKind]struct{}, len(kinds))

	for _, k := range kinds {
		_, dup := seen[k]
		require.Falsef(t, dup, "duplicate EventKind value %d", k)

		seen[k] = struct{}{}
	}

	require.Len(t, seen, len(kinds))
}

func TestEventKind_String(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   domain.EventKind
		want string
	}{
		{domain.EventPortAttached, "port_attached"},
		{domain.EventPortDetached, "port_detached"},
		{domain.EventPortErrored, "port_errored"},
		{domain.EventPortReconnectExhausted, "port_reconnect_exhausted"},
		{domain.EventDeviceBound, "device_bound"},
		{domain.EventDeviceUnbound, "device_unbound"},
		{domain.EventSessionStarted, "session_started"},
		{domain.EventSessionEnded, "session_ended"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.in.String())
		})
	}
}

func TestEventKind_FallbackString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   domain.EventKind
		want string
	}{
		{"first unnamed kind", domain.EventKind(8), "event(8)"},
		{"high unnamed kind", domain.EventKind(99), "event(99)"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.in.String())
		})
	}
}

func TestConcreteEvents_ImplementEvent(t *testing.T) {
	t.Parallel()

	now := time.Now()
	port := domain.Port{ID: 1}
	dev := domain.Device{BusID: "1-1"}
	sess := domain.Session{}

	cases := []struct {
		name string
		ev   domain.Event
		kind domain.EventKind
	}{
		{"attached", domain.PortAttachedEvent{At: now, Port: port}, domain.EventPortAttached},
		{"detached", domain.PortDetachedEvent{At: now, Port: port, Reason: "r"}, domain.EventPortDetached},
		{"errored", domain.PortErroredEvent{At: now, Port: port, Err: "e"}, domain.EventPortErrored},
		{
			"reconnect_exhausted",
			domain.PortReconnectExhaustedEvent{At: now, Port: port, Attempts: 3, LastError: "io"},
			domain.EventPortReconnectExhausted,
		},
		{"bound", domain.DeviceBoundEvent{At: now, Device: dev}, domain.EventDeviceBound},
		{"unbound", domain.DeviceUnboundEvent{At: now, Device: dev}, domain.EventDeviceUnbound},
		{"session_start", domain.SessionStartedEvent{At: now, Session: sess}, domain.EventSessionStarted},
		{"session_end", domain.SessionEndedEvent{At: now, Session: sess, Reason: "r"}, domain.EventSessionEnded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.kind, tc.ev.EventKind())
		})
	}
}
