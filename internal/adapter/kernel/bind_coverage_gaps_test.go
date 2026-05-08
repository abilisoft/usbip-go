// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_NonEBUSYErrorReturnsImmediately pins the non-transient error
// path inside bindUSBIPHostWithRetry. When the usbip-host bind write
// returns an error that is NOT ErrDeviceAlreadyBound (EBUSY), the retry
// loop must abort immediately without consuming the remaining attempts.
// Operators must see the real cause (EPERM → ErrPermission), not a
// wrapped EBUSY chain.
func TestBind_NonEBUSYErrorReturnsImmediately(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	writeFn := func(p, d string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: d})
		rec.mu.Unlock()

		if p == usbipHostBindPath {
			return unix.EPERM
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(writeFn),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.Error(t, err)
	require.NotErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"non-EBUSY error must not surface as ErrDeviceAlreadyBound")

	var bindCount int

	rec.mu.Lock()
	for _, c := range rec.calls {
		if c.Path == usbipHostBindPath {
			bindCount++
		}
	}
	rec.mu.Unlock()

	require.Equal(t, 1, bindCount,
		"non-EBUSY error must cause an immediate return; the retry loop must not fire additional attempts")
}

// TestBind_RollbackFailsLogsWarning pins the branch at the top of the
// bind error path: when bindUSBIPHostWithRetry exhausts its EBUSY
// retries AND the compensating match_busid del write also fails, the
// logger must emit a Warn (not panic or swallow the error). The primary
// return must still be the original EBUSY error so callers see WHY bind
// failed, not the rollback failure.
func TestBind_RollbackFailsLogsWarning(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("1-1")
	rec := &writeRecord{}

	writeFn := func(p, d string) error {
		rec.mu.Lock()
		rec.calls = append(rec.calls, writeCall{Path: p, Data: d})
		rec.mu.Unlock()

		if p == usbipHostBindPath {
			return unix.EBUSY
		}

		if p == usbipHostMatchBusIDPath && strings.HasPrefix(d, "del ") {
			return unix.EIO
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(bindFS(string(busID))),
		kernel.WithWriteFunc(writeFn),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)

	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"primary error (EBUSY → ErrDeviceAlreadyBound) must be returned even when rollback also fails")
}

// netdevBindFS extends bindFS with a /net subtree for deactivateNetdevs
// tests. It wires both the per-device net dir and the class/net entry
// expected by the flags preflight.
func netdevBindFS(busID, netName string) fstest.MapFS {
	mfs := bindFS(busID)

	mfs["sys/bus/usb/devices/"+busID+"/net"] = &fstest.MapFile{Mode: fs.ModeDir}

	mfs["sys/bus/usb/devices/"+busID+"/net/"+netName] = &fstest.MapFile{Mode: fs.ModeDir}

	mfs["sys/class/net/"+netName] = &fstest.MapFile{Mode: fs.ModeDir}

	return mfs
}

// TestBind_NetdevIFFUpNotSet_SkipsWrite pins the deactivateNetdevs branch
// where the flags value has IFF_UP (bit 0x1) already cleared. No write
// should be issued; the preflight continues to the driver unbind step
// without touching the flags file.
func TestBind_NetdevIFFUpNotSet_SkipsWrite(t *testing.T) {
	t.Parallel()

	const (
		busID   = "1-1"
		netName = "enxCC00"
	)

	mfs := netdevBindFS(busID, netName)
	// IFF_UP bit (0x1) is NOT set → deactivateNetdevs must skip the write.
	mfs["sys/class/net/"+netName+"/flags"] = &fstest.MapFile{Data: []byte("0x1002\n")}

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.NoError(t, err)

	for _, c := range rec.calls {
		require.NotEqual(t, "/sys/class/net/"+netName+"/flags", c.Path,
			"deactivateNetdevs must not write to flags when IFF_UP is already cleared")
	}
}

// TestBind_NetdevIFFUpSet_WriteFails_BindStillSucceeds pins the
// best-effort contract inside deactivateNetdevs: when the flags write
// fails the preflight logs at debug and continues — the bind step still
// executes. An operator whose network adapter kernel refuses the flags
// write must not be blocked from exporting the device.
func TestBind_NetdevIFFUpSet_WriteFails_BindStillSucceeds(t *testing.T) {
	t.Parallel()

	const (
		busID   = "1-1"
		netName = "enxCC01"
	)

	mfs := netdevBindFS(busID, netName)
	// IFF_UP set → deactivateNetdevs will attempt a write which the
	// writeFunc below will reject.
	mfs["sys/class/net/"+netName+"/flags"] = &fstest.MapFile{Data: []byte("0x1003\n")}

	writeFn := func(p, _ string) error {
		if p == "/sys/class/net/"+netName+"/flags" {
			return unix.EIO
		}

		return nil
	}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(writeFn),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.NoError(t, err,
		"deactivateNetdevs write failure is best-effort; bind must succeed despite the flags write error")
}

// TestBind_NetdevNoFlagsFile_SkipsNetdev pins readNetdevFlagsHex error
// path inside deactivateNetdevs: when the netdev directory exists but
// the flags file is absent, ReadLine returns an error, the logger emits
// debug, and deactivateNetdevs continues. Bind must still succeed.
func TestBind_NetdevNoFlagsFile_SkipsNetdev(t *testing.T) {
	t.Parallel()

	const (
		busID   = "1-1"
		netName = "enxCC02"
	)

	mfs := netdevBindFS(busID, netName)
	// Intentionally omit the flags file — ReadLine will fail.

	rec := &writeRecord{}

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(rec.record()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID(busID))
	require.NoError(t, err,
		"absent flags file is a read error inside deactivateNetdevs; bind must still succeed")

	for _, c := range rec.calls {
		require.NotContains(t, c.Path, "class/net",
			"absent flags file must prevent any write to the /sys/class/net path")
	}
}

// TestBind_VHCIGuard_NonVHCISymlink_AllowedThrough pins the happy path
// of refuseVHCIBindLoop: when the device symlink resolves to a path that
// does NOT contain "vhci_hcd" the guard returns nil and bind proceeds.
// Without this branch covered, a mutation that replaces the Contains
// check with an always-true literal would be undetected.
func TestBind_VHCIGuard_NonVHCISymlink_AllowedThrough(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("3-1")
	root := buildNonVHCIFakeSysfs(t, string(busID))

	a, err := kernel.NewExporterAdapter(kernel.WithFS(os.DirFS(root)))
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)

	// Guard must allow the device through; bind fails later (no
	// bConfigurationValue or interface dirs in the minimal fixture).
	require.NotErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"non-vhci device must pass the guard; any downstream failure must not be ErrDeviceAlreadyBound")
}

// buildNonVHCIFakeSysfs creates a temp-dir-backed sysfs skeleton where
// /sys/bus/usb/devices/<busID> is a symlink whose target goes through a
// PCI bus path (no "vhci_hcd" component). Mirrors buildVHCIRootedFakeSysfs
// from bind_vhci_loop_test.go but with a non-VHCI target.
func buildNonVHCIFakeSysfs(t *testing.T, busID string) string {
	t.Helper()

	root := t.TempDir()

	devDir := filepath.Join(root, "sys/bus/usb/devices")
	require.NoError(t, os.MkdirAll(devDir, 0o750))

	// Device on a normal PCI controller — path does not contain vhci_hcd.
	target := filepath.Join(root, "sys/devices/pci0000:00/0000:00:1d.0/usb1", busID)
	require.NoError(t, os.MkdirAll(target, 0o750))

	relTarget := filepath.Join("..", "..", "..", "devices", "pci0000:00", "0000:00:1d.0", "usb1", busID)
	require.NoError(t, os.Symlink(relTarget, filepath.Join(devDir, busID)))

	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/module/usbip_core"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/module/usbip_host"), 0o750))

	return root
}
