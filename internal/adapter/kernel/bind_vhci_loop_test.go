// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestBind_RefusesVHCIBindLoop pins upstream's bind-loop guard
// (tools/usb/usbip/src/usbip_bind.c bind_device): if the device is
// itself an importer-side stub of an already-attached remote (its
// sysfs devpath traces through vhci_hcd), Bind must refuse rather
// than unbind the current driver and corrupt the user's existing
// attachment.
//
// Uses a real on-disk fakeroot via os.DirFS so symlinks survive — the
// fstest.MapFS path returns fs.ErrNotExist on Readlink which would
// silently bypass the guard. The real adapter resolves symlinks via
// the production sysfs writer the same way.
func TestBind_RefusesVHCIBindLoop(t *testing.T) {
	t.Parallel()

	busID := domain.BusID("3-1")

	root := buildVHCIRootedFakeSysfs(t, string(busID))

	a, err := kernel.NewExporterAdapter(kernel.WithFS(os.DirFS(root)))
	require.NoError(t, err)

	err = a.Bind(context.Background(), busID)
	require.Error(t, err,
		"Bind must refuse a device whose devpath traces through vhci_hcd to avoid the bind loop")
	require.ErrorIs(t, err, domain.ErrDeviceAlreadyBound,
		"vhci-loop refusal must surface as ErrDeviceAlreadyBound — a re-export attempt of an already-attached device")
}

// buildVHCIRootedFakeSysfs creates a t.TempDir-backed sysfs skeleton
// where /sys/bus/usb/devices/<busID> is a symlink whose relative
// target traverses /sys/devices/platform/vhci_hcd.0. Mirrors the
// shape Linux exposes for an attached remote device.
func buildVHCIRootedFakeSysfs(t *testing.T, busID string) string {
	t.Helper()

	root := t.TempDir()

	devDir := filepath.Join(root, "sys/bus/usb/devices")
	require.NoError(t, os.MkdirAll(devDir, 0o750))

	// The actual device dir, rooted under vhci_hcd as the kernel
	// would do for an importer-side stub.
	target := filepath.Join(root, "sys/devices/platform/vhci_hcd.0/usb1", busID)
	require.NoError(t, os.MkdirAll(target, 0o750))

	// Symlink: /sys/bus/usb/devices/<busID> -> ../../../devices/...
	relTarget := filepath.Join("..", "..", "..", "devices", "platform", "vhci_hcd.0", "usb1", busID)
	require.NoError(t, os.Symlink(relTarget, filepath.Join(devDir, busID)))

	// Modules required for the preflight check to pass.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/module/usbip_core"), 0o750))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sys/module/usbip_host"), 0o750))

	return root
}
