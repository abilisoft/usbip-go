// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"context"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

func TestProbeKernelModulesCancellationReturnsFullShape(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	modules, err := usbip.ProbeKernelModules(ctx)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, map[string]usbip.ModuleState{
		usbip.KernelModuleUSBIPCore: usbip.ModuleStateUnknown,
		usbip.KernelModuleVHCIHCD:   usbip.ModuleStateUnknown,
		usbip.KernelModuleUSBIPHost: usbip.ModuleStateUnknown,
	}, modules)
}
