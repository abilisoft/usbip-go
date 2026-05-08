//go:build linux

package kernel

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// netlinkUeventBufSize is the socket receive buffer size used by the
// kernel-provided netlink listener. 32 KiB matches the kernel-default
// uevent send buffer; larger buffers are silently clamped.
const netlinkUeventBufSize = 32 * 1024

// netlinkUdevGroup is the multicast group the udev daemon subscribes
// to. Joining this group lets us see the same uevent stream udev does.
const netlinkUdevGroup = 1

// realNetlinkSocket is the production NetlinkSocket implementation. It
// wraps a raw kernel-owned NETLINK_KOBJECT_UEVENT file descriptor. The
// parser and fan-out machinery on top of Receive() lives in Task 4.10;
// the socket open/close primitive is stable and implemented here so
// NewEventsAdapter has a real default.
type realNetlinkSocket struct {
	fd int
}

// Receive blocks until one uevent payload is delivered, then returns
// the raw NUL-separated KEY=VALUE buffer. Task 4.10 lifts parsing into
// Subscribe; this method deliberately returns the raw bytes.
func (s *realNetlinkSocket) Receive() ([]byte, error) {
	buf := make([]byte, netlinkUeventBufSize)

	n, _, err := unix.Recvfrom(s.fd, buf, 0)
	if err != nil {
		return nil, fmt.Errorf("recvfrom netlink: %w", err)
	}

	return buf[:n], nil
}

// Close releases the socket fd. Safe to call from any goroutine.
func (s *realNetlinkSocket) Close() error {
	err := unix.Close(s.fd)
	if err != nil {
		return fmt.Errorf("close netlink socket: %w", err)
	}

	return nil
}

// openRealNetlinkSocket opens and binds a real
// AF_NETLINK/NETLINK_KOBJECT_UEVENT socket. The concrete-typed return
// lets callers that embed the dialer in a NetlinkDialer function value
// remain linter-legal (returning the interface type is reserved for
// the NetlinkDialer contract itself).
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

	return &realNetlinkSocket{fd: fd}, nil
}
