// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestErrMapMatrix covers every row of spec §6.4's errno → sentinel
// table plus the path-kind classification from §11.5.4. The test
// drives the exported ClassifyErrno helper directly so every mapping
// can be exercised without a real syscall.
func TestErrMapMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		path   string
		errno  unix.Errno
		want   error
		rejectedDomain []error // sentinels that MUST NOT appear in the chain
	}{
		{
			name:  "ENOENT on sysfs device path → ErrDeviceNotFound",
			path:  "/sys/bus/usb/devices/1-1.2/idVendor",
			errno: unix.ENOENT,
			want:  domain.ErrDeviceNotFound,
			rejectedDomain: []error{domain.ErrKernelModuleMissing},
		},
		{
			name:  "ENOENT on driver path → ErrKernelModuleMissing",
			path:  "/sys/bus/usb/drivers/usbip-host/bind",
			errno: unix.ENOENT,
			want:  domain.ErrKernelModuleMissing,
			rejectedDomain: []error{domain.ErrDeviceNotFound},
		},
		{
			name:  "ENOENT on controller path → ErrKernelModuleMissing",
			path:  "/sys/devices/platform/vhci_hcd.0/attach",
			errno: unix.ENOENT,
			want:  domain.ErrKernelModuleMissing,
		},
		{
			name:  "ENOENT on module path → ErrKernelModuleMissing",
			path:  "/sys/module/vhci_hcd",
			errno: unix.ENOENT,
			want:  domain.ErrKernelModuleMissing,
		},
		{
			name:  "EACCES → ErrPermission",
			path:  "/sys/bus/usb/devices/1-1/idVendor",
			errno: unix.EACCES,
			want:  domain.ErrPermission,
		},
		{
			name:  "EPERM → ErrPermission",
			path:  "/sys/bus/usb/drivers/usbip-host/bind",
			errno: unix.EPERM,
			want:  domain.ErrPermission,
		},
		{
			name:  "EBUSY on bind → ErrDeviceAlreadyBound",
			path:  "/sys/bus/usb/drivers/usbip-host/bind",
			errno: unix.EBUSY,
			want:  domain.ErrDeviceAlreadyBound,
		},
		{
			name:  "ENODEV → ErrDeviceNotFound",
			path:  "/sys/devices/platform/vhci_hcd.0/attach",
			errno: unix.ENODEV,
			want:  domain.ErrDeviceNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := kernel.ClassifyErrno(tc.path, tc.errno)
			require.ErrorIs(t, err, tc.want)

			for _, rej := range tc.rejectedDomain {
				require.NotErrorIs(t, err, rej,
					"case %q must NOT wrap %v", tc.name, rej)
			}
		})
	}
}

// TestErrMapMatrix_UnknownPassThrough confirms errnos not in the
// mapping table surface raw.
func TestErrMapMatrix_UnknownPassThrough(t *testing.T) {
	t.Parallel()

	cases := []unix.Errno{unix.EIO, unix.EINTR, unix.ETIMEDOUT, unix.ENOSYS}
	for _, e := range cases {
		err := kernel.ClassifyErrno("/sys/bus/usb/devices/1-1/idVendor", e)
		require.ErrorIs(t, err, e, "raw errno must still be matchable")
	}
}
