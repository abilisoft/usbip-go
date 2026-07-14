// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package usbip

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// moduleSysfsRoot is the sysfs root for loaded kernel modules. Paired
// with probeOneAt so tests can inject a tmpdir root without mutating
// a package-level variable.
const moduleSysfsRoot = "/sys/module"

// probeKernelModulesPlatform reports which USB/IP kernel modules from the
// security-release-quality and operations-observability OpenSpec documents
// appear loaded according to /sys/module. The returned map
// always contains the three expected keys; the per-module value is
// one of the three ModuleState constants.
//
// Only ctx cancellation produces an error; per-module stat failures
// are classified into the tri-state value (Missing / Unknown) and
// never break the rest of the probe.
func probeKernelModulesPlatform(
	ctx context.Context,
	out map[string]ModuleState,
) (map[string]ModuleState, error) {
	return probeKernelModulesWith(ctx, out, func(name string) ModuleState {
		return probeOneAt(moduleSysfsRoot, name)
	})
}

func probeKernelModulesWith(
	ctx context.Context,
	out map[string]ModuleState,
	probe func(string) ModuleState,
) (map[string]ModuleState, error) {
	for _, name := range probedModuleNames() {
		err := moduleProbeContextError(ctx)
		if err != nil {
			return out, err
		}

		out[name] = probe(name)
	}

	return out, nil
}

// probeOneAt stats <root>/<name> with os.Stat. Tests exercise alternative stat
// results through the pure probeOneAtWithStat helper rather than mutating
// package-global process state.
func probeOneAt(root, name string) ModuleState {
	return probeOneAtWithStat(root, name, os.Stat)
}

// probeOneAtWithStat classifies one module path through the supplied stat
// dependency.
//
//   - stat OK                 → Loaded
//   - ENOENT / fs.ErrNotExist → Missing
//   - any other error         → Unknown
func probeOneAtWithStat(
	root string,
	name string,
	stat func(string) (fs.FileInfo, error),
) ModuleState {
	path := filepath.Join(root, name)

	_, err := stat(path)

	switch {
	case err == nil:
		return ModuleStateLoaded
	case errors.Is(err, fs.ErrNotExist):
		return ModuleStateMissing
	default:
		return ModuleStateUnknown
	}
}
