//go:build linux

package kernel

import (
	"context"
	"fmt"
	"io/fs"
	"path"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// usbDriversRoot is the parent directory of per-driver sysfs entries,
// e.g. usbhid lives at /sys/bus/usb/drivers/usbhid. Not a constant in
// sysfs_paths.go because the dynamic <driver> component is substituted
// in at runtime.
const usbDriversRoot = "/sys/bus/usb"

// usbDriversSubdir is the driver subdirectory under usbDriversRoot, so
// unbind writes target /sys/bus/usb/drivers/<drv>/unbind.
const usbDriversSubdir = "drivers"

// ifaceDriverNameFile is the sysfs attribute that — when the driver
// symlink is unavailable (tests, fstest.MapFS) — names the current
// interface driver. Production resolves the symlink at the same path.
const ifaceDriverNameFile = "driver_name"

// Bind performs the three-write sequence required by usbip-host:
//  1. unbind current driver from the primary interface
//  2. add the busID to match_busid
//  3. bind usbip-host to the primary interface
//
// Module preflight runs first so runtime module loss surfaces as
// ErrKernelModuleMissing rather than ErrDeviceNotFound on the first
// write.
func (a *ExporterAdapter) Bind(ctx context.Context, busID domain.BusID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	iface := ifaceSuffix(busID)

	driver, err := a.currentDriver(iface)
	if err != nil {
		return err
	}

	err = a.writeClassified(driverPath(driver, SysfsDriverUnbind), iface)
	if err != nil {
		return err
	}

	err = a.writeClassified(
		path.Join(SysfsUSBIPHostDriver, SysfsMatchBusID),
		MatchBusIDAddPrefix+string(busID),
	)
	if err != nil {
		return err
	}

	err = a.writeClassified(path.Join(SysfsUSBIPHostDriver, SysfsDriverBind), iface)
	if err != nil {
		return err
	}

	return nil
}

// Unbind reverses Bind: writes to usbip-host/unbind, removes the busID
// from match_busid, then triggers a default-driver rebind.
func (a *ExporterAdapter) Unbind(ctx context.Context, busID domain.BusID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	iface := ifaceSuffix(busID)

	err = a.writeClassified(path.Join(SysfsUSBIPHostDriver, SysfsDriverUnbind), iface)
	if err != nil {
		return err
	}

	err = a.writeClassified(
		path.Join(SysfsUSBIPHostDriver, SysfsMatchBusID),
		MatchBusIDDelPrefix+string(busID),
	)
	if err != nil {
		return err
	}

	err = a.writeClassified(path.Join(SysfsUSBIPHostDriver, SysfsRebind), string(busID))
	if err != nil {
		return err
	}

	return nil
}

// writeClassified invokes the injected WriteFunc and routes any
// returned error through the path-aware errno classifier. Shared
// between bind, unbind, attach/detach, and export/disconnect so every
// kernel-write call site observes a domain sentinel on recognised
// errnos.
func (a *commonAdapter) writeClassified(target, data string) error {
	err := a.write(target, data)
	if err != nil {
		return classifyFSErr("write", target, err)
	}

	return nil
}

// ifaceSuffix returns "<busID>:1.0", the primary interface sysfs
// suffix used by every bind/unbind operation. Matches upstream
// libsrc/usbip_host_driver.c.
func ifaceSuffix(busID domain.BusID) string {
	return string(busID) + VHCIIfaceSuffix
}

// driverPath returns "/sys/bus/usb/drivers/<driver>/<attr>".
func driverPath(driver, attr string) string {
	return path.Join(usbDriversRoot, usbDriversSubdir, driver, attr)
}

// currentDriver identifies the interface driver currently bound to
// iface. Production reads the "driver" symlink target under
// /sys/bus/usb/devices/<iface>/driver and takes its basename. MapFS
// cannot emit symlinks so tests stage a driver_name text file instead;
// we consult both in order.
//
// Error-distinction contract (spec §6.4 + §4.4):
//   - Interface directory missing → ErrDeviceNotFound (via classifyErrno
//     in the sysfs read helpers).
//   - Interface directory present, but no driver attachment (neither
//     driver_name file nor driver symlink) → ErrDeviceNotBound. The
//     device exists; it just is not bound to any driver.
//   - Both paths' reads fail with unexpected (non-ENOENT) errno →
//     surface verbatim.
func (a *ExporterAdapter) currentDriver(iface string) (string, error) {
	ifaceDir := path.Join(SysfsUSBDevices, iface)

	// Preferred: read a plain file whose contents name the driver.
	// MapFS supports this cleanly; production sysfs emits the driver
	// name via a driver_name attribute that the kernel populates for
	// drivers registered with a usb_driver{.name=...} record.
	nameFile := path.Join(ifaceDir, "driver", ifaceDriverNameFile)

	name, nameErr := ReadLine(a.fs, nameFile)
	if nameErr == nil && name != "" {
		return name, nil
	}

	// Fallback: probe the symlink target directly by reading it as a
	// file (production sysfs only — MapFS does not support this).
	// ReadLinkFS is the stdlib 1.26 interface; if fsys is OS-backed we
	// try it via the rlFS interface.
	link, linkErr := readLink(a.fs, path.Join(ifaceDir, "driver"))
	if linkErr == nil && link != "" {
		return path.Base(link), nil
	}

	// Both lookups failed. Before collapsing the result into
	// ErrDeviceNotBound, distinguish "present but absent driver" (the
	// only case that shape is legitimate for) from "present but read
	// failed with a real I/O or permission fault". The driver-attachment
	// conclusion is only sound when both failures reduce to ENOENT-ish —
	// a non-ENOENT errno means we cannot see the driver state, not that
	// there is none, and surfacing ErrDeviceNotBound there would hide a
	// permission or I/O fault behind a wrong error class.
	if !isMissing(nameErr) {
		return "", nameErr
	}

	if linkErr != nil && !isMissing(linkErr) {
		return "", linkErr
	}

	// Both failures are absence-shaped. Distinguish "device missing
	// entirely" from "device present but unbound" via the interface
	// directory itself. Present + no driver → ErrDeviceNotBound; missing
	// → ErrDeviceNotFound (nameErr already carries that via the ENOENT
	// classifier, but we re-wrap for a uniform message).
	_, statErr := fs.Stat(a.fs, fsPathFromAbs(ifaceDir))
	if statErr == nil {
		return "", fmt.Errorf("%w: no driver attached to %s", domain.ErrDeviceNotBound, iface)
	}

	if nameErr != nil {
		return "", nameErr
	}

	return "", fmt.Errorf("%w: %s", domain.ErrDeviceNotFound, iface)
}

// readLink reads a symlink through fsys when the fs.FS implementation
// supports it. os.DirFS satisfies fs.ReadLinkFS (Go 1.26+); MapFS does
// not, in which case readLink returns fs.ErrNotExist so the caller can
// fall through.
func readLink(fsys fs.FS, p string) (string, error) {
	if rl, ok := fsys.(fs.ReadLinkFS); ok {
		target, err := rl.ReadLink(fsPathFromAbs(p))
		if err != nil {
			return "", fmt.Errorf("readlink %q: %w", p, err)
		}

		return target, nil
	}

	return "", fs.ErrNotExist
}
