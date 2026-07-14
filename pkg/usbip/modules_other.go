// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package usbip

import "context"

// probeKernelModulesPlatform on non-Linux has no sysfs to consult. Every
// module reports ModuleStateUnknown (not Missing) because the platform
// lacks the signal entirely — claiming "missing" would be a lie.
func probeKernelModulesPlatform(
	ctx context.Context,
	out map[string]ModuleState,
) (map[string]ModuleState, error) {
	if err := moduleProbeContextError(ctx); err != nil {
		return out, err
	}

	return out, nil
}
