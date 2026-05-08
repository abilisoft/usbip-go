//go:build !linux

package usbip

// probeOneAtForTestInvoke returns ModuleStateUnknown on non-Linux
// builds; the Linux-only probeOneAt is absent from these builds.
func probeOneAtForTestInvoke(_, _ string) ModuleState {
	return ModuleStateUnknown
}
