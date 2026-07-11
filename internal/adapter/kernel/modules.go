// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"context"
	"fmt"
	"io/fs"
	"path"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// checkModules probes /sys/module/<name>/ for each name. Present
// returns nil; absent returns ErrKernelModuleMissing wrapped with the
// first missing module name plus a modprobe hint so stderr output is
// directly actionable.
func checkModules(fsys fs.FS, names ...string) error {
	for _, name := range names {
		rel := fsPathFromAbs(path.Join(SysfsModuleDir, name))

		_, err := fs.Stat(fsys, rel)
		if err == nil {
			continue
		}

		if errorsIsAny(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: run `sudo modprobe %s`",
				domain.ErrKernelModuleMissing, name)
		}

		return classifyFSErr("stat module", path.Join(SysfsModuleDir, name), err)
	}

	return nil
}

// ModulesAvailable probes usbip_core + vhci_hcd — the importer-side
// module set. Runs at startup and before each importer operation so
// runtime module disappearance described by the security-release-quality and
// operations-observability OpenSpec documents is surfaced with a
// clear error.
func (a *ImporterAdapter) ModulesAvailable(_ context.Context) error {
	return checkModules(a.fs, ModuleUsbipCore, ModuleVHCIHCD)
}

// ModulesAvailable probes usbip_core + usbip_host — the exporter-side
// module set.
func (a *ExporterAdapter) ModulesAvailable(_ context.Context) error {
	return checkModules(a.fs, ModuleUsbipCore, ModuleUsbipHost)
}
