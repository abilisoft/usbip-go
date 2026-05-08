//go:build !linux

package usbip

import "fmt"

// newDefaultImporter refuses construction on non-Linux: the kernel
// adapter carries //go:build linux so no concrete ImporterKernel is
// compiled into the binary. Returning ErrKernelModuleMissing lets the
// caller surface a deterministic, classifiable error rather than a
// runtime panic when the first Attach runs.
func newDefaultImporter(_ []ImporterOption) (*Importer, error) {
	return nil, fmt.Errorf("usbip.NewImporter: %w (supported platforms: linux)", ErrKernelModuleMissing)
}

// newDefaultExporter mirrors newDefaultImporter for the Exporter role.
func newDefaultExporter(_ []ExporterOption) (*Exporter, error) {
	return nil, fmt.Errorf("usbip.NewExporter: %w (supported platforms: linux)", ErrKernelModuleMissing)
}
