// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// makeFS returns a MapFS rooted at /, with arbitrary files added for
// each entry. Keys are absolute paths; the map strips the leading slash
// to match fs.FS semantics (rooted at "." i.e. the first path element).
func makeFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}

	for p, d := range files {
		rel := p
		if len(rel) > 0 && rel[0] == '/' {
			rel = rel[1:]
		}

		m[rel] = &fstest.MapFile{Data: []byte(d)}
	}

	return m
}

// makeFSWithDirs extends makeFS by also creating empty directories at
// each supplied path. Needed to simulate kernel module presence under
// /sys/module/<name>/ where the directory's mere existence is the
// signal.
func makeFSWithDirs(files map[string]string, dirs ...string) fstest.MapFS {
	m := makeFS(files)

	for _, d := range dirs {
		rel := d
		if len(rel) > 0 && rel[0] == '/' {
			rel = rel[1:]
		}

		m[rel] = &fstest.MapFile{Mode: fs.ModeDir}
	}

	return m
}

func TestReadLine_Trimmed(t *testing.T) {
	t.Parallel()

	mfs := makeFS(map[string]string{
		"/sys/bus/usb/devices/1-1/product": "  Wireless Mouse  \n",
	})
	got, err := kernel.ReadLine(mfs, "/sys/bus/usb/devices/1-1/product")
	require.NoError(t, err)
	require.Equal(t, "Wireless Mouse", got)
}

func TestReadHex16_WithAndWithoutPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want uint16
	}{
		{"0x0951\n", 0x0951},
		{"0951\n", 0x0951},
		{"  0x04e8  \n", 0x04e8},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			mfs := makeFS(map[string]string{testUSBDeviceVendorPath: tc.raw})

			got, err := kernel.ReadHex16(mfs, testUSBDeviceVendorPath)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestReadUint_Decimal(t *testing.T) {
	t.Parallel()

	mfs := makeFS(map[string]string{"/sys/bus/usb/devices/1-1/busnum": "42\n"})
	got, err := kernel.ReadUint(mfs, "/sys/bus/usb/devices/1-1/busnum")
	require.NoError(t, err)
	require.EqualValues(t, 42, got)
}

func TestListDirEntries_SortedNames(t *testing.T) {
	t.Parallel()

	mfs := makeFSWithDirs(
		nil,
		"/sys/bus/usb/devices/1-1",
		"/sys/bus/usb/devices/1-1.2",
		"/sys/bus/usb/devices/usb1",
	)
	names, err := kernel.ListDirEntries(mfs, "/sys/bus/usb/devices")
	require.NoError(t, err)
	require.Equal(t, []string{testRootBusID, "1-1.2", "usb1"}, names)
}

// Errno → sentinel mapping tests. The classify step must distinguish
// device paths (ErrDeviceNotFound) from driver/module paths
// (ErrKernelModuleMissing) when the underlying syscall reports ENOENT.

func TestReadLine_DevicePath_ENOENT_MapsToDeviceNotFound(t *testing.T) {
	t.Parallel()

	mfs := makeFS(nil)
	_, err := kernel.ReadLine(mfs, testUSBDeviceVendorPath)
	require.ErrorIs(t, err, domain.ErrDeviceNotFound)
}

func TestReadLine_DriverPath_ENOENT_MapsToModuleMissing(t *testing.T) {
	t.Parallel()

	mfs := makeFS(nil)
	_, err := kernel.ReadLine(mfs, testUSBIPHostBindPath)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

func TestReadLine_VHCIPath_ENOENT_MapsToModuleMissing(t *testing.T) {
	t.Parallel()

	mfs := makeFS(nil)
	_, err := kernel.ReadLine(mfs, testVHCIAttachPath)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

func TestReadLine_ModulePath_ENOENT_MapsToModuleMissing(t *testing.T) {
	t.Parallel()

	mfs := makeFS(nil)
	_, err := kernel.ReadLine(mfs, "/sys/module/vhci_hcd")
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

// TestClassifyErrno_EACCES and EPERM map to ErrPermission regardless of
// path. We exercise this via the exported classifier directly because
// testing/fstest.MapFS cannot surface EACCES on reads.
func TestClassifyErrno_EACCES_EPERM(t *testing.T) {
	t.Parallel()

	eacces := kernel.ClassifyErrno(testUSBDeviceVendorPath, unix.EACCES)
	require.ErrorIs(t, eacces, domain.ErrPermission)

	eperm := kernel.ClassifyErrno(testUSBDeviceVendorPath, unix.EPERM)
	require.ErrorIs(t, eperm, domain.ErrPermission)
}

func TestClassifyErrno_EBUSY_MapsToAlreadyBound(t *testing.T) {
	t.Parallel()

	err := kernel.ClassifyErrno(testUSBIPHostBindPath, unix.EBUSY)
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound)
}

func TestClassifyErrno_ENODEV_MapsToDeviceNotFound(t *testing.T) {
	t.Parallel()

	err := kernel.ClassifyErrno(testVHCIAttachPath, unix.ENODEV)
	require.ErrorIs(t, err, domain.ErrDeviceNotFound)
}

// TestClassifyErrno_UnknownPassThrough confirms that errnos outside the
// mapping matrix surface unchanged — upper layers may introduce new
// mappings later, but the classifier must not swallow unexpected
// errors under the wrong sentinel.
func TestClassifyErrno_UnknownPassThrough(t *testing.T) {
	t.Parallel()

	err := kernel.ClassifyErrno(testUSBDeviceVendorPath, unix.EIO)
	require.ErrorIs(t, err, unix.EIO)
	require.NotErrorIs(t, err, domain.ErrDeviceNotFound)
	require.NotErrorIs(t, err, domain.ErrKernelModuleMissing)
}

// TestReadHex16_MalformedWraps confirms a non-hex payload returns an
// error that wraps nothing from the domain sentinel set (it's a parse
// error, not a syscall error).
func TestReadHex16_MalformedWraps(t *testing.T) {
	t.Parallel()

	mfs := makeFS(map[string]string{testUSBDeviceVendorPath: "not-hex\n"})

	_, err := kernel.ReadHex16(mfs, testUSBDeviceVendorPath)
	require.Error(t, err)
	require.ErrorContains(t, err, "idVendor")
}

// TestReadUint_Negative surfaces a parse-error path explicitly.
func TestReadUint_Negative(t *testing.T) {
	t.Parallel()

	mfs := makeFS(map[string]string{"/sys/bus/usb/devices/1-1/busnum": "-1\n"})

	_, err := kernel.ReadUint(mfs, "/sys/bus/usb/devices/1-1/busnum")
	require.Error(t, err)
	require.NotErrorIs(t, err, domain.ErrDeviceNotFound)
}
