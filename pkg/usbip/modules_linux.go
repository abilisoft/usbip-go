//go:build linux

package usbip

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Kernel module names probed by ProbeKernelModules. The set matches the
// spec §11.5.4 triple plus the cmd/usbipd status JSON schema; exporter
// binaries need usbip_core + usbip_host, importer binaries need
// usbip_core + vhci_hcd, and usbipd's status endpoint reports all three
// because an operator looking at /status may be sharing a host between
// both roles.
const (
	moduleStatusLoaded  = "loaded"
	moduleStatusMissing = "missing"
)

// probedModuleNames is the canonical triple returned by
// ProbeKernelModules. Exposed as a function (not a var) so tests can't
// mutate the slice.
func probedModuleNames() []string {
	return []string{"usbip_core", "vhci_hcd", "usbip_host"}
}

// moduleSysfsRoot is the sysfs root for loaded kernel modules. It is
// declared via a getter so no mutable package-level variable is
// exposed (which would trip gochecknoglobals); Phase 9 test helpers
// that point the probe at a tmpdir will install a replacement
// implementation through an option on NewExporter / NewImporter rather
// than mutating a global.
const moduleSysfsRoot = "/sys/module"

// ProbeKernelModules reports which of the §11.5.4 USB/IP kernel modules
// appear loaded according to /sys/module. The returned map always
// contains the three expected keys ("usbip_core", "vhci_hcd",
// "usbip_host"); a value of "loaded" means the module's sysfs entry
// exists, "missing" means it doesn't.
//
// The function never returns an error today — every sysfs-stat failure
// collapses into "missing" because that's what operators actually want:
// a best-effort snapshot. The error return is kept on the signature so
// a future Phase 9 expansion (permission errors, non-Linux stubbing)
// can surface failure without a breaking-change bump.
func ProbeKernelModules(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(probedModuleNames()))

	for _, name := range probedModuleNames() {
		err := ctx.Err()
		if err != nil {
			return out, fmt.Errorf("probe kernel modules: %w", err)
		}

		out[name] = probeOne(name)
	}

	return out, nil
}

// probeOne stats /sys/module/<name>. Present → "loaded", absent →
// "missing". Non-ENOENT errors (e.g. EACCES) are reported as "missing"
// too — the public contract is a binary signal; diagnosing why is the
// next tier of operator tooling.
func probeOne(name string) string {
	path := filepath.Join(moduleSysfsRoot, name)

	_, err := os.Stat(path)
	switch {
	case err == nil:
		return moduleStatusLoaded
	case errors.Is(err, fs.ErrNotExist):
		return moduleStatusMissing
	default:
		return moduleStatusMissing
	}
}
