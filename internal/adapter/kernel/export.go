// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"context"
	"net"
	"path"
	"strconv"
	"syscall"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// ExportOnConn extracts the OS fd from conn and writes its decimal
// ASCII form to /sys/bus/usb/devices/<busid>/usbip_sockfd. Mirror of
// ImporterAdapter.AttachRemote: after the kernel-side sockfd_lookup
// succeeds the kernel owns a ref on the socket; callers still own
// their own fd and MUST close it themselves when the session ends —
// this adapter never closes the caller's conn (v1 contract §5.4 item 4).
func (a *ExporterAdapter) ExportOnConn(ctx context.Context, conn net.Conn, busID domain.BusID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	fd, err := extractFD(conn)
	if err != nil {
		return err
	}

	// fd is a dup'd descriptor — close it after the kernel sysfs write
	// on all exit paths (kernel obtains its own socket ref via sockfd_lookup).
	defer func() { _ = syscall.Close(int(fd)) }()

	return a.writeClassified(
		path.Join(SysfsUSBDevices, string(busID), SysfsUsbipSockfd),
		strconv.FormatUint(uint64(fd), 10),
	)
}

// Disconnect writes "-1" to /sys/bus/usb/devices/<busid>/usbip_sockfd.
// This triggers SDEV_EVENT_DOWN kernel-side; the export session drops
// cleanly. Do NOT close the caller's conn as a substitute — kernel
// owns the socket and a local close alone accomplishes nothing useful
// for the remote end.
func (a *ExporterAdapter) Disconnect(ctx context.Context, busID domain.BusID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	return a.writeClassified(
		path.Join(SysfsUSBDevices, string(busID), SysfsUsbipSockfd),
		UsbipSockfdDisconnect,
	)
}
