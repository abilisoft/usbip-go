// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip

import (
	"context"
	"encoding/json"
	"fmt"
)

// probedModuleNames is the canonical module triple returned on every platform.
// A function returns a fresh slice so callers and tests cannot mutate shared
// package state.
func probedModuleNames() []string {
	return []string{KernelModuleUSBIPCore, KernelModuleVHCIHCD, KernelModuleUSBIPHost}
}

// unknownModuleStates constructs the shape-stable baseline for every probe.
// Platform implementations overwrite entries they can classify; cancellation
// leaves every unprobed entry explicitly Unknown instead of omitting keys.
func unknownModuleStates() map[string]ModuleState {
	out := make(map[string]ModuleState, len(probedModuleNames()))
	for _, name := range probedModuleNames() {
		out[name] = ModuleStateUnknown
	}

	return out
}

// ProbeKernelModules reports the canonical USB/IP module triple on every
// platform. The returned map always contains all three keys, including when
// ctx is cancelled; platform code may replace Unknown values as observations
// complete.
func ProbeKernelModules(ctx context.Context) (map[string]ModuleState, error) {
	out := unknownModuleStates()

	err := moduleProbeContextError(ctx)
	if err != nil {
		return out, err
	}

	return probeKernelModulesPlatform(ctx, out)
}

func moduleProbeContextError(ctx context.Context) error {
	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("probe kernel modules: %w", err)
	}

	return nil
}

// ModuleState is the tri-state classification of a USB/IP kernel
// module probe result. A two-state "loaded" / "missing" design
// silently collapses EACCES / EIO / any-other-error into "missing",
// masking the difference between "probe was blocked" and "probe
// proved the module is absent"; this tri-state preserves that
// distinction.
//
// JSON marshaling emits the lowercase string form so the operations-observability
// and json-contracts OpenSpec status
// schema retains its stable `{"usbip_core": "loaded"}` shape.
type ModuleState int

// Canonical Linux USB/IP kernel module names used as ProbeKernelModules keys.
const (
	KernelModuleUSBIPCore = "usbip_core"
	KernelModuleVHCIHCD   = "vhci_hcd"
	KernelModuleUSBIPHost = "usbip_host"
)

// ModuleState constants. Order is public API: the iota placement
// determines what the zero value means. ModuleStateUnknown is 0 so a
// forgotten-to-populate map entry flags as Unknown rather than the
// potentially misleading Loaded or Missing.
const (
	ModuleStateUnknown ModuleState = iota
	ModuleStateLoaded
	ModuleStateMissing
)

// String returns the lowercase wire form of the state. Invalid
// (out-of-range) values render as "unknown" for a safe fallback.
func (s ModuleState) String() string {
	switch s {
	case ModuleStateLoaded:
		return "loaded"
	case ModuleStateMissing:
		return "missing"
	case ModuleStateUnknown:
		return "unknown"
	}

	return "unknown"
}

// MarshalJSON renders the lowercase string form so the status JSON
// schema stays a stable string mapping. json.Marshaler is the right
// seam here because the caller's map[string]ModuleState should
// otherwise marshal as an integer.
func (s ModuleState) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(s.String())
	if err != nil {
		return nil, fmt.Errorf("marshal ModuleState: %w", err)
	}

	return data, nil
}
