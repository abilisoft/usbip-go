// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

// netlinkRecvTimeout bounds a single blocked Receive call before the
// kernel returns EAGAIN so the dispatcher's run loop can check its
// stop channel. Shutdown latency is bounded by this value. 1s keeps
// the worst case comfortably under test teardown budgets while adding
// no measurable overhead to the hot path.
const netlinkRecvTimeout = 1 * time.Second

// realNetlinkSocket is the production NetlinkSocket implementation.
//
// Lives in its own file so the project's coverage gate can carve it
// out without scattering coverage-ignore annotations: every method
// here dispatches a real AF_NETLINK syscall whose error branches
// cannot be synthesised hermetically. The fake NetlinkSocket
// injected via WithNetlinkSocket exercises the dispatcher logic;
// the integration suite exercises this concrete socket. See
// `.testcoverage.yaml` exclude list.
type realNetlinkSocket struct {
	fd int
}

// Receive blocks until one uevent payload is delivered OR the socket's
// SO_RCVTIMEO expires. A timeout surfaces as unix.EAGAIN so the
// dispatcher's run loop can re-check its stop channel without the
// recvfrom syscall holding an unclosable file descriptor.
func (s *realNetlinkSocket) Receive() ([]byte, error) {
	buf := make([]byte, netlinkUeventBufSize)

	n, _, err := unix.Recvfrom(s.fd, buf, 0)
	if err != nil {
		return nil, fmt.Errorf("recvfrom netlink: %w", err)
	}

	return buf[:n], nil
}

// Close releases the socket fd.
func (s *realNetlinkSocket) Close() error {
	err := unix.Close(s.fd)
	if err != nil {
		return fmt.Errorf("close netlink socket: %w", err)
	}

	return nil
}

// openRealNetlinkSocket opens and binds a real
// AF_NETLINK/NETLINK_KOBJECT_UEVENT socket. SO_RCVTIMEO is armed so
// the dispatcher's run loop sees periodic wakes, because plain
// unix.Close on a blocked Recvfrom does not unblock it on Linux;
// without the timeout a shutdown would deadlock on the final
// `<-d.done` in tearDownDispatcher.
func openRealNetlinkSocket() (*realNetlinkSocket, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("open netlink socket: %w", err)
	}

	sa := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: netlinkUdevGroup,
	}

	err = unix.Bind(fd, sa)
	if err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("bind netlink socket: %w", err)
	}

	tv := unix.NsecToTimeval(netlinkRecvTimeout.Nanoseconds())

	err = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	if err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("set SO_RCVTIMEO: %w", err)
	}

	return &realNetlinkSocket{fd: fd}, nil
}

// defaultNetlinkDialer is the production netlink-socket factory. The
// closure adapts the concrete-typed openRealNetlinkSocket into the
// interface-valued NetlinkDialer contract. Excluded with the
// real-socket type.
func defaultNetlinkDialer() NetlinkDialer {
	return func() (NetlinkSocket, error) {
		s, err := openRealNetlinkSocket()
		if err != nil {
			return nil, err
		}

		return s, nil
	}
}
