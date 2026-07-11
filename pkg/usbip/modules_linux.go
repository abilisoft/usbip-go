// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package usbip

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// probedModuleNames is the canonical §11.5.4 triple returned by
// ProbeKernelModules. Exposed as a function (not a var) so tests
// cannot mutate the slice.
func probedModuleNames() []string {
	return []string{KernelModuleUSBIPCore, KernelModuleVHCIHCD, KernelModuleUSBIPHost}
}

// moduleSysfsRoot is the sysfs root for loaded kernel modules. Paired
// with probeOneAt so tests can inject a tmpdir root without mutating
// a package-level variable.
const moduleSysfsRoot = "/sys/module"

// ProbeKernelModules reports which of the §11.5.4 USB/IP kernel
// modules appear loaded according to /sys/module. The returned map
// always contains the three expected keys; the per-module value is
// one of the three ModuleState constants.
//
// Only ctx cancellation produces an error; per-module stat failures
// are classified into the tri-state value (Missing / Unknown) and
// never break the rest of the probe.
func ProbeKernelModules(ctx context.Context) (map[string]ModuleState, error) {
	out := make(map[string]ModuleState, len(probedModuleNames()))

	for _, name := range probedModuleNames() {
		err := ctx.Err()
		if err != nil {
			return out, fmt.Errorf("probe kernel modules: %w", err)
		}

		out[name] = probeOneAt(moduleSysfsRoot, name)
	}

	return out, nil
}

// probeStatFn is the os.Stat indirection probeOneAt goes through.
// Tests swap it via swapProbeStatFn to simulate EACCES or other stat
// errors without touching filesystem permissions — chmod-based
// simulation runs afoul of gosec G302 on dir traversal bits and is
// brittle under parallel t.TempDir cleanup. probeStatMu serialises
// the read/write so parallel tests that swap and other tests that
// call probeOneAt do not data-race under -race.
var (
	probeStatMu sync.RWMutex
	probeStatFn = os.Stat
)

// probeOneAt stats <root>/<name> via probeStatFn. Used by
// ProbeKernelModules with the production moduleSysfsRoot and by
// export_test.go with a tmpdir + injected stat error.
//
//   - stat OK                 → Loaded
//   - ENOENT / fs.ErrNotExist → Missing
//   - any other error         → Unknown
func probeOneAt(root, name string) ModuleState {
	path := filepath.Join(root, name)

	probeStatMu.RLock()

	stat := probeStatFn

	probeStatMu.RUnlock()

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
