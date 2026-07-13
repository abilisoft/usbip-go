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
// this adapter never closes the caller's conn, as required by the kernel-adapter fd-handoff contract.
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

// ExportSessionActive reads usbip_host's authoritative per-device
// connection state. Linux transitions usbip_status from SDEV_ST_USED
// back to SDEV_ST_AVAILABLE when a remote peer disconnects, but does
// not emit an exporter-side VHCI detach uevent for that transition.
func (a *ExporterAdapter) ExportSessionActive(ctx context.Context, busID domain.BusID) (bool, error) {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return false, err
	}

	status, err := ReadUint(a.fs, path.Join(SysfsUSBDevices, string(busID), SysfsUsbipStatus))
	if err != nil {
		return false, err
	}

	return status == usbipStatusUsed, nil
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
