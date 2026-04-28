// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBindStatusSocket_MissingParentDirSurfacesENOENT pins the failure
// mode an operator hits when invoking the daemon manually without
// pre-creating the status-socket parent directory. systemd's
// RuntimeDirectory=usbip-go directive creates /run/usbip-go on unit
// start, but a hand-run daemon (`sudo usbipd-go`) bypasses systemd and
// the default --status-socket=/run/usbip-go/status.sock fails on the
// flock OpenFile when the parent dir is absent.
//
// This test pins:
//   - the daemon does NOT silently MkdirAll a path under /run/
//   - the surfaced error names the missing path so the operator can fix it
//   - the failure happens at bindStatusSocket, not later, so the daemon
//     never enters a half-started state
func TestBindStatusSocket_MissingParentDirSurfacesENOENT(t *testing.T) {
	t.Parallel()

	// Use a TempDir-derived path with a NON-existent intermediate
	// component. /run is privileged so the test cannot exercise the
	// production default literally, but the failure mode is identical
	// for any missing-parent-dir scenario.
	tmp := t.TempDir()
	missingParent := filepath.Join(tmp, "definitely-does-not-exist", "status.sock")

	lis, err := bindStatusSocket(context.Background(), missingParent, "")
	require.Error(t, err,
		"bindStatusSocket must fail when the parent directory is absent — operators must see this, not a half-started daemon")
	require.Nil(t, lis,
		"failed bind must return a nil listener so callers cannot accidentally Serve on a half-bound UDS")
	require.Contains(t, err.Error(), missingParent+".lock",
		"error message must name the lock file path so the operator knows which directory is missing")
	require.ErrorIs(t, err, fs.ErrNotExist,
		"error must wrap fs.ErrNotExist so callers can errors.Is-classify the missing-parent case")
}
