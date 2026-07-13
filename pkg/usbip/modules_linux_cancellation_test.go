// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package usbip_test

import (
	"context"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

func TestProbeKernelModulesMidProbeCancellationKeepsFullShape(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	probeCalls := 0

	got, err := usbip.ProbeKernelModulesWithForTest(ctx, func(string) usbip.ModuleState {
		probeCalls++

		cancel()

		return usbip.ModuleStateMissing
	})

	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, probeCalls)
	require.Equal(t, usbip.ModuleStateMissing, got[usbip.KernelModuleUSBIPCore])
	require.Equal(t, usbip.ModuleStateUnknown, got[usbip.KernelModuleVHCIHCD])
	require.Equal(t, usbip.ModuleStateUnknown, got[usbip.KernelModuleUSBIPHost])
}
