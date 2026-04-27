// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"time"

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

// usbipHostBindRetryAttempts caps how many times Bind will retry the
// usbip-host bind sysfs write on EBUSY. EBUSY at this step is almost
// always transient — the kernel needs a moment to release the device's
// refcount after the previous interface driver is unbound (network
// stack draining queued packets, audio buffers flushing, etc.). Five
// total attempts at the backoff schedule below cover the typical
// 200-400ms drain window on Linux 6.x.
const usbipHostBindRetryAttempts = 5

// usbipHostBindRetryBackoffMs is the doubling schedule between retries
// in milliseconds. Worst case (5 retries) sleeps ~3.1s before
// surfacing EBUSY — long enough for any reasonable kernel-side drain,
// short enough for an operator running `usbip-go bind` interactively
// to perceive as "took a beat" rather than "hung".
var usbipHostBindRetryBackoffMs = []int{0, 100, 200, 400, 800}

// Bind performs the three-write sequence required by usbip-host:
//  1. unbind current driver from the primary interface (iface
//     "<busid>:<bConfigurationValue>.0")
//  2. add the busID to usbip-host/match_busid
//  3. bind usbip-host to the device (BARE busid; usbip-host is a
//     usb_device_driver and matches at usb_device level)
//
// Step 3 failure rolls back step 2 (match_busid del) so the busid
// table is not poisoned. Step 2 failure is left as-is — the unbind
// in step 1 is preserved so the operator can rebind manually,
// matching upstream usbip_bind.c semantics.
//
// Module preflight runs first so runtime module loss surfaces as
// ErrKernelModuleMissing rather than ErrDeviceNotFound on the first
// write.
func (a *ExporterAdapter) Bind(ctx context.Context, busID domain.BusID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	err = a.refuseVHCIBindLoop(busID)
	if err != nil {
		return err
	}

	iface, err := a.ifaceSuffix(busID)
	if err != nil {
		return err
	}

	// USB-to-Ethernet / cellular dongles register one or more netdevs
	// (cdc_ether, r8152, ...). Those netdevs hold a usb_device
	// refcount as long as IFF_UP is set, so the subsequent
	// usbip-host bind step returns EBUSY. Walk the per-device
	// /sys/bus/usb/devices/<busid>/net/<name> directories and clear
	// IFF_UP on each before unbinding the driver, so the operator
	// no longer has to chase `ip link set <enxXX> down` manually.
	a.deactivateNetdevs(busID)

	// currentDriver returns ErrDeviceNotBound when the interface has
	// no driver attached — common right after a previous half-failed
	// bind (driver unbound, usbip-host did not claim) or after a
	// fresh hot-plug where the kernel has not yet probed. In that
	// case skip the unbind step (nothing to unbind) and proceed
	// straight to match_busid + usbip-host bind. Other errors
	// (ErrDeviceNotFound, permission, I/O) still surface.
	driver, err := a.currentDriver(iface)
	switch {
	case errors.Is(err, domain.ErrDeviceNotBound):
		a.logger.Debug("bind: interface has no driver attached; skipping unbind step",
			"busid", busID, "iface", iface)
	case err != nil:
		return err
	default:
		// Old driver is typically an interface-level usb_driver
		// (cdc_ether, usbhid, etc.) — kernel sysfs unbind_store
		// looks the device up by name on the bus, so it must match
		// the bound device's name. For interface drivers that means
		// iface ("1-1:2.0").
		err = a.writeClassified(driverPath(driver, SysfsDriverUnbind), iface)
		if err != nil {
			return err
		}
	}

	err = a.writeClassified(
		path.Join(SysfsUSBIPHostDriver, SysfsMatchBusID),
		MatchBusIDAddPrefix+string(busID),
	)
	if err != nil {
		return err
	}

	// usbip-host is a usb_device_driver (drivers/usb/usbip/stub_dev.c
	// declares `struct usb_device_driver stub_driver`). The kernel's
	// driver bind/unbind sysfs handler looks up the target by name on
	// the bus and only proceeds when dev->driver matches. For a
	// device-level driver that means the BARE busid ("1-1"), not the
	// iface — passing iface here would find the usb_interface, fail
	// the dev->driver match, and surface ENODEV.
	err = a.bindUSBIPHostWithRetry(ctx, busID)
	if err != nil {
		// Roll back the match_busid add so the busid table is not
		// poisoned with an entry that has no driver attached.
		// Mirrors upstream usbip_bind.c bind_device error path.
		// Rollback failure is logged but does not mask the original
		// error; the caller wants to see WHY bind failed first.
		rbErr := a.writeClassified(
			path.Join(SysfsUSBIPHostDriver, SysfsMatchBusID),
			MatchBusIDDelPrefix+string(busID),
		)
		if rbErr != nil {
			a.logger.Warn("bind rollback (match_busid del) failed",
				"busid", busID, "rollback_err", rbErr, "primary_err", err)
		}

		return err
	}

	return nil
}

// Unbind reverses Bind: gracefully disconnects any active importer
// session, writes to usbip-host/unbind, removes the busID from
// match_busid, then triggers a default-driver rebind.
//
// The pre-disconnect step is crucial: writing -1 to the per-device
// usbip_sockfd attribute triggers SDEV_EVENT_DOWN in the kernel and
// drops any in-flight URBs cleanly. Without it, the subsequent
// usbip-host/unbind write blocks indefinitely while the kernel
// waits for the importer socket to drain — operators saw
// `usbip-go unbind` hang.
//
// The pre-disconnect failure is non-fatal because a freshly-bound
// device with no active session has no sockfd attribute (or the
// attribute returns ENODEV); the unbind sequence continues either
// way.
func (a *ExporterAdapter) Unbind(ctx context.Context, busID domain.BusID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	// Best-effort sockfd disconnect to unblock any pending URB drain
	// before the driver unbind. Errors here are logged, not returned —
	// see godoc above for why.
	preErr := a.write(
		path.Join(SysfsUSBDevices, string(busID), SysfsUsbipSockfd),
		UsbipSockfdDisconnect,
	)
	if preErr != nil {
		a.logger.Debug("unbind pre-disconnect write returned error (typically benign — no active session)",
			"busid", busID, "err", preErr)
	}

	// usbip-host operates at usb_device level, so unbind takes BARE
	// busid ("1-1"), not iface. See the matching comment in Bind.
	unbindErr := a.writeClassified(path.Join(SysfsUSBIPHostDriver, SysfsDriverUnbind), string(busID))

	// match_busid del and the rebind trigger run regardless of the
	// unbind result so the device table is not stuck with a stale
	// entry and the original kernel driver gets a chance to take the
	// device back. The unbind error (if any) wins as the primary
	// return so the operator sees the root cause; secondary errors
	// are logged.
	matchErr := a.writeClassified(
		path.Join(SysfsUSBIPHostDriver, SysfsMatchBusID),
		MatchBusIDDelPrefix+string(busID),
	)

	rebindErr := a.writeClassified(path.Join(SysfsUSBIPHostDriver, SysfsRebind), string(busID))

	switch {
	case unbindErr != nil:
		if matchErr != nil {
			a.logger.Warn("unbind: match_busid del failed after primary unbind error",
				"busid", busID, "match_err", matchErr, "primary_err", unbindErr)
		}

		if rebindErr != nil {
			a.logger.Warn("unbind: rebind trigger failed after primary unbind error",
				"busid", busID, "rebind_err", rebindErr, "primary_err", unbindErr)
		}

		return unbindErr
	case matchErr != nil:
		if rebindErr != nil {
			a.logger.Warn("unbind: rebind trigger failed after match_busid del error",
				"busid", busID, "rebind_err", rebindErr, "primary_err", matchErr)
		}

		return matchErr
	case rebindErr != nil:
		return rebindErr
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

// bindUSBIPHostWithRetry writes the busid to usbip-host/bind, retrying
// on EBUSY (ErrDeviceAlreadyBound). EBUSY at this step is almost
// always a transient refcount drain — the previous driver finishing
// its release. Surfacing EBUSY without retry forces operators into
// manual rebind dances that the daemon can perform itself.
//
// On retry, deactivateNetdevs runs again so a NetworkManager-style
// helper that re-set IFF_UP between attempts cannot indefinitely
// prevent the bind. Retries respect ctx cancellation; a Ctrl-C
// during the backoff sleep returns ctx.Err() instead of the EBUSY
// chain.
func (a *ExporterAdapter) bindUSBIPHostWithRetry(ctx context.Context, busID domain.BusID) error {
	target := path.Join(SysfsUSBIPHostDriver, SysfsDriverBind)

	var lastErr error

	for attempt := range usbipHostBindRetryAttempts {
		if backoff := usbipHostBindRetryBackoffMs[attempt]; backoff > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-a.clock.After(time.Duration(backoff) * time.Millisecond):
			}

			a.logger.Debug("bind: retrying usbip-host bind after EBUSY",
				"busid", busID, "attempt", attempt+1, "backoff_ms", backoff)

			// Re-clear IFF_UP in case a network manager bounced the
			// netdev back up between attempts. Cheap (sysfs walk +
			// at most a few writes) and robust against the
			// "operator's wifi keeps re-enabling itself" class of
			// failure.
			a.deactivateNetdevs(busID)
		}

		err := a.writeClassified(target, string(busID))
		if err == nil {
			return nil
		}

		lastErr = err

		// Only EBUSY (mapped to ErrDeviceAlreadyBound by the errno
		// classifier) is transient. ENODEV, EPERM, etc. surface
		// immediately so operators see the real cause.
		if !errors.Is(err, domain.ErrDeviceAlreadyBound) {
			return err
		}
	}

	return lastErr
}

// iffUp is the IFF_UP bit in /sys/class/net/<name>/flags. Mirrors
// include/uapi/linux/if.h. We toggle this bit only — every other
// flag (IFF_BROADCAST, IFF_MULTICAST, …) is preserved so the netdev
// can be brought back UP later without losing its prior config.
const iffUp = 0x1

// deactivateNetdevs walks the per-device netdev sysfs subtree and
// clears IFF_UP on each interface. Best-effort: read failures and
// write failures are logged at debug, never returned. Devices
// without a netdev (storage, HID, audio, …) have no /net/
// subdirectory and the walker is a no-op for them.
//
// Required because USB-to-network drivers (cdc_ether, r8152,
// ax88179_178a) keep a usb_device refcount while IFF_UP is set,
// which cascades into EBUSY at the usbip-host bind step. Operators
// previously had to run `ip link set <enxXX> down` manually before
// every bind; this routine performs the same operation by writing
// a flags value with the IFF_UP bit cleared to /sys/class/net/<name>/flags.
func (a *ExporterAdapter) deactivateNetdevs(busID domain.BusID) {
	netRoot := path.Join(SysfsUSBDevices, string(busID), "net")

	names, err := ListDirEntries(a.fs, netRoot)
	if err != nil {
		// No /net subdir is the common case — most USB devices are
		// not network adapters. Nothing to do.
		return
	}

	for _, name := range names {
		flagsPath := path.Join("/sys/class/net", name, "flags")

		// /sys/class/net/<name>/flags is a hex string ("0x1003"), so
		// ReadUint's base-10 parser fails. Hand-roll the parse.
		current, rerr := readNetdevFlagsHex(a.fs, flagsPath)
		if rerr != nil {
			a.logger.Debug("bind preflight: read netdev flags failed",
				"netdev", name, "err", rerr)

			continue
		}

		if current&iffUp == 0 {
			continue
		}

		cleared := current &^ iffUp

		werr := a.write(flagsPath, fmt.Sprintf("0x%x", cleared))
		if werr != nil {
			a.logger.Debug("bind preflight: clear netdev IFF_UP failed (continuing — bind may EBUSY)",
				"netdev", name, "err", werr)

			continue
		}

		a.logger.Debug("bind preflight: deactivated netdev to free device for usbip-host",
			"busid", busID, "netdev", name)
	}
}

// readNetdevFlagsHex reads /sys/class/net/<name>/flags as a hex
// string. The kernel emits values like "0x1003\n"; ReadUint's
// base-10 parser rejects them.
func readNetdevFlagsHex(fsys fs.FS, p string) (uint32, error) {
	line, err := ReadLine(fsys, p)
	if err != nil {
		return 0, err
	}

	stripped := strings.TrimPrefix(strings.TrimPrefix(line, "0x"), "0X")

	v, perr := strconv.ParseUint(stripped, hexRadix, dec10Bits)
	if perr != nil {
		return 0, fmt.Errorf("parse netdev flags %q: %w", p, perr)
	}

	return uint32(v), nil
}

// vhciDevPathMarker is the substring upstream's bind_device looks
// for in the device's sysfs devpath to detect that a busid is itself
// the importer-side stub of an already-attached remote device.
// Mirrors USBIP_VHCI_DRV_NAME in tools/usb/usbip/src/usbip_common.h.
const vhciDevPathMarker = "vhci_hcd"

// refuseVHCIBindLoop rejects busID when its sysfs devpath traces
// through vhci_hcd. Without this guard, Bind on an importer-side
// stub of an already-attached remote unbinds the user's existing
// vhci-driven attachment in step 1 (driver unbind), then fails in
// step 3 because usbip-host cannot bind a device sitting under
// vhci_hcd. The user is left with neither device.
//
// Failure modes:
//
//   - fs.ErrNotExist or "not a symlink" / EINVAL: guard
//     inapplicable. Either there is no entry at the busid path, or
//     the FS does not store this entry as a symlink (MapFS in unit
//     tests, or a non-sysfs fakeroot). Production sysfs always
//     provides the symlink, so these branches only fire in tests.
//   - fs.ErrPermission and any other unexpected error: FAIL
//     CLOSED. We cannot prove the device is NOT under vhci_hcd,
//     so the only safe action is to surface the underlying error
//     and refuse to begin the destructive bind sequence. A
//     permissive bypass here would let an ACL fault on an
//     importer-side device silently corrupt the user's existing
//     attachment.
func (a *ExporterAdapter) refuseVHCIBindLoop(busID domain.BusID) error {
	link, err := readLink(a.fs, path.Join(SysfsUSBDevices, string(busID)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		// "Invalid argument" means the entry exists but is not a
		// symlink — readLink on a regular dir / file. Sysfs in
		// production always uses symlinks here so this is a
		// test-fixture shape, not a runtime concern.
		if errors.Is(err, fs.ErrInvalid) || strings.Contains(err.Error(), "invalid argument") {
			return nil
		}

		return fmt.Errorf("vhci-loop guard: readlink %s: %w", busID, err)
	}

	if strings.Contains(link, vhciDevPathMarker) {
		return fmt.Errorf("%w: %s is itself attached via vhci_hcd; refusing bind to avoid loop",
			domain.ErrDeviceAlreadyBound, busID)
	}

	return nil
}

// ifaceSuffix returns "<busID>:<bConfigurationValue>.0", the primary
// interface sysfs suffix for the device's currently-active
// configuration. Matches upstream libsrc/usbip_host_driver.c which
// formats the iface as "%s:%d.0" with the device's
// bConfigurationValue. Hardcoding ":1.0" breaks for any device whose
// active configuration is not 1 (e.g. many USB-to-Ethernet adapters
// default to config 2).
func (a *commonAdapter) ifaceSuffix(busID domain.BusID) (string, error) {
	configValue, err := readU16Attr(a.fs, path.Join(SysfsUSBDevices, string(busID), devAttrConfigValue))
	if err != nil {
		return "", err
	}

	if configValue == 0 {
		return "", fmt.Errorf("%w: device %s reports bConfigurationValue=0 (no active configuration)",
			domain.ErrDeviceNotBound, busID)
	}

	if configValue > byteMax {
		return "", fmt.Errorf("%w: %s/bConfigurationValue=%d (exceeds u8)",
			errSysfsValueOutOfRange, busID, configValue)
	}

	return fmt.Sprintf("%s:%d.0", busID, configValue), nil
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
// Error-distinction contract (v1 contract §6.4 + §4.4):
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
