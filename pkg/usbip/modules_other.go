//go:build !linux

package usbip

import "context"

// Kernel module name triple reported by ProbeKernelModules on every
// platform. Non-Linux builds have no sysfs; every module reports
// "missing" so the status JSON remains shape-stable.
const (
	moduleStatusLoaded  = "loaded"
	moduleStatusMissing = "missing"
)

// probedModuleNames mirrors the Linux list byte-for-byte.
func probedModuleNames() []string {
	return []string{"usbip_core", "vhci_hcd", "usbip_host"}
}

// ProbeKernelModules returns the §11.5.4 kernel-module triple marked
// "missing" on every non-Linux platform. The error return is never
// populated today; it exists for parity with the Linux signature.
func ProbeKernelModules(_ context.Context) (map[string]string, error) {
	out := make(map[string]string, len(probedModuleNames()))
	for _, name := range probedModuleNames() {
		out[name] = moduleStatusMissing
	}

	return out, nil
}
