// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package usbip

import "context"

// probeOneAtForTestInvoke returns ModuleStateUnknown on non-Linux
// builds; the Linux-only probeOneAt is absent from these builds.
func probeOneAtForTestInvoke(_, _ string) ModuleState {
	return ModuleStateUnknown
}

// ProbeKernelModulesPlatformForTest exposes the non-Linux platform boundary so
// black-box tests can verify cancellation after the common baseline is built.
func ProbeKernelModulesPlatformForTest(ctx context.Context) (map[string]ModuleState, error) {
	return probeKernelModulesPlatform(ctx, unknownModuleStates())
}
