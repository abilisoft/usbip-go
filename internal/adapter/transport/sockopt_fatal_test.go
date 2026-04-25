// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package transport_test

import (
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/transport"
)

// TestIsSockoptFatalRejectsDeadConn pins the classification boundary:
// SetNoDelay returning net.ErrClosed, ENOTCONN, or EBADF means the
// connection is no longer usable (concurrent Close, peer RST, or a
// double-close race). Dial must not hand such a conn back to the
// caller after warn-and-continue; the next Write would fail with
// "use of closed network connection" far from the real cause.
func TestIsSockoptFatalRejectsDeadConn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		err   error
		fatal bool
	}{
		{"ErrClosed", net.ErrClosed, true},
		{"ENOTCONN", syscall.ENOTCONN, true},
		{"EBADF", syscall.EBADF, true},
		{"EINVAL", syscall.EINVAL, false},
		{"EPERM", syscall.EPERM, false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.fatal, transport.IsSockoptFatalForTest(tc.err))
		})
	}
}
