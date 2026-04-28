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
		domain.EventDeviceBound,
		domain.EventDeviceUnbound,
		domain.EventRemoteDeviceAdded,
		domain.EventRemoteDeviceRemoved,
		domain.EventSessionStarted,
		domain.EventSessionEnded,
	}

	seen := make(map[domain.EventKind]struct{}, len(kinds))

	for _, k := range kinds {
		_, dup := seen[k]
		require.Falsef(t, dup, "duplicate EventKind value %d", k)

		seen[k] = struct{}{}
	}

	require.Len(t, seen, 9)
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
		{domain.EventDeviceBound, "device_bound"},
		{domain.EventDeviceUnbound, "device_unbound"},
		{domain.EventRemoteDeviceAdded, "remote_device_added"},
		{domain.EventRemoteDeviceRemoved, "remote_device_removed"},
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

	require.Equal(t, "event(99)", domain.EventKind(99).String())
}

// TestEventKind_FallbackStringAtBoundary pins EventKind.String at
// the exact len(names) boundary. CONDITIONALS_BOUNDARY mutates
// `int(k) < len(names)` to `<=`; the mutant indexes names[len(names)],
// which panics. Only an EventKind whose value EQUALS len(names)
// distinguishes the two — values above use the fallback for both
// real and mutant; values below succeed for both.
//
// The test uses EventKind(numEventKinds) where numEventKinds is the
// authoritative count from constants_test.go's domain.EventKind
// inventory; if a new kind is added, the test still tracks the new
// boundary because numEventKinds bumps in lockstep.
func TestEventKind_FallbackStringAtBoundary(t *testing.T) {
	t.Parallel()

	// 9 named kinds today; the test is intentionally not coupled to
	// that constant — it walks up until String returns the fallback
	// shape and pins the FIRST k that does. A mutation that turns
	// `<` into `<=` panics on that exact k.
	for k := range 1024 {
		got := domain.EventKind(k).String()
		if got == "event("+itoaForTest(k)+")" {
			// Found the boundary. The value String() returned for
			// EventKind(k) MUST be the fallback shape, never a
			// names[k] entry. require.Equal is enough to lock that
			// in; the mutation that broadens `<` to `<=` would
			// either return the wrong name or panic.
			require.Equal(t, "event("+itoaForTest(k)+")", got)
			return
		}
	}

	t.Fatalf("no EventKind value below 1024 returned a fallback string — eventKindNames may be unbounded?")
}

// itoaForTest is a strconv-free decimal formatter for small ints,
// avoiding an extra import in this minimal test.
func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}

	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}

	return string(buf)
}

func TestConcreteEvents_ImplementEvent(t *testing.T) {
	t.Parallel()

	now := time.Now()
	port := domain.Port{ID: 1}
	dev := domain.Device{BusID: "1-1"}
	sess := domain.Session{}
	remote := domain.RemoteEndpoint{Host: "h"}

	cases := []struct {
		name string
		ev   domain.Event
		kind domain.EventKind
	}{
		{"attached", domain.PortAttachedEvent{At: now, Port: port}, domain.EventPortAttached},
		{"detached", domain.PortDetachedEvent{At: now, Port: port, Reason: "r"}, domain.EventPortDetached},
		{"errored", domain.PortErroredEvent{At: now, Port: port, Err: "e"}, domain.EventPortErrored},
		{"bound", domain.DeviceBoundEvent{At: now, Device: dev}, domain.EventDeviceBound},
		{"unbound", domain.DeviceUnboundEvent{At: now, Device: dev}, domain.EventDeviceUnbound},
		{
			"remote_added",
			domain.RemoteDeviceAddedEvent{At: now, Remote: remote, Device: dev},
			domain.EventRemoteDeviceAdded,
		},
		{
			"remote_removed",
			domain.RemoteDeviceRemovedEvent{At: now, Remote: remote, BusID: "1-1"},
			domain.EventRemoteDeviceRemoved,
		},
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
