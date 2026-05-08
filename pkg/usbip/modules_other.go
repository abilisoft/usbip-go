// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package usbip

import "context"

// probedModuleNames mirrors the Linux list byte-for-byte so the JSON
// schema is shape-stable across platforms.
func probedModuleNames() []string {
	return []string{"usbip_core", "vhci_hcd", "usbip_host"}
}

// ProbeKernelModules on non-Linux has no sysfs to consult. Every
// module reports ModuleStateUnknown (not Missing) because the platform
// lacks the signal entirely — claiming "missing" would be a lie.
func ProbeKernelModules(_ context.Context) (map[string]ModuleState, error) {
	out := make(map[string]ModuleState, len(probedModuleNames()))
	for _, name := range probedModuleNames() {
		out[name] = ModuleStateUnknown
	}

	return out, nil
}
