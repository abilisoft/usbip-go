// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package usbip_test

import (
	"context"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

func TestProbeKernelModulesNonLinuxReturnsFullUnknownShape(t *testing.T) {
	t.Parallel()

	want := map[string]usbip.ModuleState{
		usbip.KernelModuleUSBIPCore: usbip.ModuleStateUnknown,
		usbip.KernelModuleVHCIHCD:   usbip.ModuleStateUnknown,
		usbip.KernelModuleUSBIPHost: usbip.ModuleStateUnknown,
	}

	modules, err := usbip.ProbeKernelModules(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, modules)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	modules, err = usbip.ProbeKernelModulesPlatformForTest(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, want, modules)
}
