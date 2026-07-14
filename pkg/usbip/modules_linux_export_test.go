// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package usbip

import (
	"context"
	"io/fs"
)

func probeOneAtForTestInvoke(root, name string) ModuleState {
	return probeOneAt(root, name)
}

// ProbeOneAtWithStatForTest exposes the pure stat-injection helper so parallel
// black-box tests can classify failures without mutating process-global state.
func ProbeOneAtWithStatForTest(
	root string,
	name string,
	stat func(string) (fs.FileInfo, error),
) ModuleState {
	return probeOneAtWithStat(root, name, stat)
}

// ProbeKernelModulesWithForTest exposes the Linux probe loop with a controlled
// per-module probe so black-box tests can cancel between entries.
func ProbeKernelModulesWithForTest(
	ctx context.Context,
	probe func(string) ModuleState,
) (map[string]ModuleState, error) {
	return probeKernelModulesWith(ctx, unknownModuleStates(), probe)
}
