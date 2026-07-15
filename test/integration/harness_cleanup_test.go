// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunGadgetCleanup_MissingRootIsNoOp(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "missing-gadget")
	require.NoError(t, runGadgetCleanup(root))
	require.NoError(t, runGadgetCleanup(root))
}

func TestVUDCDeviceReleaseIsIdempotentAndReturnsCleanupError(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("cleanup failed")
	calls := 0
	device := &VUDCDevice{
		cleanup: idempotentCleanup(func() error {
			calls++

			return sentinel
		}),
	}

	require.ErrorIs(t, device.Release(), sentinel)
	require.ErrorIs(t, device.Release(), sentinel)
	require.Equal(t, 1, calls)

	var nilDevice *VUDCDevice
	require.NoError(t, nilDevice.Release())
}
