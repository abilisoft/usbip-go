//go:build linux

package main

import (
	"context"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDetectAlreadyRunningSurfacesUnknownErrors pins the invariant
// that a dial failing with anything other than ECONNREFUSED (stale
// socket) or ENOENT (fresh start) must NOT be silently reclassified
// as "stale, safe to unlink". The flock in bindStatusSocket is the
// authoritative exclusion, but detectAlreadyRunning's output feeds
// the unlink decision; promoting EACCES, context.DeadlineExceeded,
// or any other dial error into "stale" would wipe a live peer's
// socket on a transient slow dial or a permission glitch.
func TestDetectAlreadyRunningSurfacesUnknownErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
	}{
		{"EACCES", syscall.EACCES},
		{"EPERM", syscall.EPERM},
		{"ELOOP", syscall.ELOOP},
		{"ENOTDIR", syscall.ENOTDIR},
		{"context deadline", context.DeadlineExceeded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dial := func(_ context.Context, _, _ string) (net.Conn, error) {
				return nil, tc.err
			}

			err := detectAlreadyRunningWithDialer(context.Background(),
				"/run/usbip/test.sock", dial)

			require.Error(t, err,
				"%s must surface as a non-nil error, not be silently reclassified as stale", tc.name)
			require.ErrorIs(t, err, tc.err,
				"wrapped error chain must preserve the original errno for operator diagnostics")
		})
	}
}

// TestDetectAlreadyRunningTreatsECONNREFUSEDAsStale retains the
// accepted branch: ECONNREFUSED continues to mean "stale file, safe
// to unlink". The branch is the only genuinely-safe promotion.
func TestDetectAlreadyRunningTreatsECONNREFUSEDAsStale(t *testing.T) {
	t.Parallel()

	dial := func(_ context.Context, _, _ string) (net.Conn, error) {
		return nil, syscall.ECONNREFUSED
	}

	err := detectAlreadyRunningWithDialer(context.Background(),
		"/run/usbip/stale.sock", dial)
	require.NoError(t, err, "ECONNREFUSED must still classify as stale")
}
