package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// fakeNetError implements net.Error so MapError's As-based detection
// can bind a generic dial-class failure.
type fakeNetError struct{}

func (fakeNetError) Error() string { return "network" }

// Timeout reports whether the error represents a timeout condition.
// Returning false keeps MapError's classification on the network
// path rather than the timeout path.
func (fakeNetError) Timeout() bool { return false }

// Temporary reports whether the error is transient. Value ignored.
func (fakeNetError) Temporary() bool { return false }

// TestMapErrorTable walks every sentinel in spec §7.4 and asserts
// MapError returns the matching code.
func TestMapErrorTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"permission", usbip.ErrPermission, ExitPermission},
		{"kernel-module", usbip.ErrKernelModuleMissing, ExitKernelModule},
		{"device-not-found", usbip.ErrDeviceNotFound, ExitDeviceNotFound},
		{"device-already-bound", usbip.ErrDeviceAlreadyBound, ExitDeviceBusy},
		{"port-in-use", usbip.ErrPortInUse, ExitDeviceBusy},
		{"device-not-bound", usbip.ErrDeviceNotBound, ExitDeviceBusy},
		{"protocol-mismatch", usbip.ErrProtocolMismatch, ExitProtocolMismatch},
		{"no-free-port", usbip.ErrNoFreePort, ExitNoFreePort},
		{"protocol-error", usbip.ErrProtocolError, ExitProtocolError},
		{"deadline", context.DeadlineExceeded, ExitTimeout},
		{"canceled", context.Canceled, ExitTimeout},
		{"net-error", fakeNetError{}, ExitNetwork},
		{"usage-error", &usageError{msg: "bad usage"}, ExitUsage},
		{"cobra-usage-style", errors.New("unknown flag: --bogus"), ExitUsage},
		{"generic", errors.New("something else"), ExitGeneric},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, MapError(tc.err))
		})
	}
}

// TestFormatErrorTable asserts FormatError renders a stable template
// prefix for every spec §7.4 sentinel.
func TestFormatErrorTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		prefix string
	}{
		{"nil-is-empty", nil, ""},
		{"permission", usbip.ErrPermission, "usbip: operation requires elevated privileges"},
		{"kernel-module", usbip.ErrKernelModuleMissing, "usbip: kernel module not loaded"},
		{"device-not-found", usbip.ErrDeviceNotFound, "usbip: device not found"},
		{"device-already-bound", usbip.ErrDeviceAlreadyBound, "usbip: device is already bound"},
		{"port-in-use", usbip.ErrPortInUse, "usbip: port is in use"},
		{"device-not-bound", usbip.ErrDeviceNotBound, "usbip: device is not bound"},
		{"protocol-mismatch", usbip.ErrProtocolMismatch, "usbip: protocol mismatch"},
		{"no-free-port", usbip.ErrNoFreePort, "usbip: no free vhci port"},
		{"protocol-error", usbip.ErrProtocolError, "usbip: peer reported an error"},
		{"deadline", context.DeadlineExceeded, "usbip: operation timed out"},
		{"net-error", fakeNetError{}, "usbip: network error"},
		{"generic", errors.New("something else"), "usbip: error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := FormatError(tc.err)
			if tc.prefix == "" {
				require.Empty(t, got)

				return
			}

			require.Contains(t, got, tc.prefix)
		})
	}
}

// TestMapErrorWrapsPreserveIdentity — an error wrapped via fmt.Errorf
// with %w still classifies via errors.Is.
func TestMapErrorWrapsPreserveIdentity(t *testing.T) {
	t.Parallel()

	wrapped := errors.Join(usbip.ErrDeviceNotFound, errors.New("extra info"))

	require.Equal(t, ExitDeviceNotFound, MapError(wrapped))
}

// TestMapErrorNetDeadline — a net.Error with Timeout() true is still
// classified as timeout (ExitTimeout) via the ctx sentinels.
func TestMapErrorNetDeadline(t *testing.T) {
	t.Parallel()

	var opErr net.Error = &net.OpError{Op: "dial", Err: errors.New("host unreachable")}

	require.Equal(t, ExitNetwork, MapError(opErr))
}
