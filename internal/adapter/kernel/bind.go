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
// to perceive as "took a beat" rather than "hung". Declared as a
// fixed-size array (not a slice) so it is a true constant table —
// gochecknoglobals tolerates [N]T literals as compile-time constants.
var usbipHostBindRetryBackoffMs = [usbipHostBindRetryAttempts]int{0, 100, 200, 400, 800}

// preflightBind runs the non-mutating checks before Bind starts
// touching sysfs: kernel module availability, vhci-loop refusal, hub
// guard, and already-exported short-circuit. Extracted from Bind() to
// keep the main flow under the gocognit threshold.
func (a *ExporterAdapter) preflightBind(ctx context.Context, busID domain.BusID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	err = a.refuseVHCIBindLoop(busID)
	if err != nil {
		return err
	}

	// Hub guard. usbip-host's stub_probe rejects hubs at
	// drivers/usb/usbip/stub_dev.c:347-351; upstream usbip_bind.c
	// reads bDeviceClass and refuses at libsrc lines 82-91. We refuse
	// HERE — before any unbind write — because detaching the generic
	// "usb" device driver from a hub disconnects every downstream
	// device hanging off it. By the time the kernel rejects, damage
	// is done.
	err = a.refuseHubDevice(busID)
	if err != nil {
		return err
	}

	// Already-bound short-circuit. Must run BEFORE ifaceSuffix
	// because once the device is bound to usbip-host the kernel's USB
	// core has already unconfigured it (bConfigurationValue=0); a
	// later ifaceSuffix would surface a misleading
	// "no active configuration" ErrDeviceNotBound. Returns nil for
	// any non-usbip-host driver so the pipeline continues.
	return a.checkAlreadyExported(busID)
}

// Bind performs the four-write sequence required by usbip-host:
//  1. unbind current driver from the primary interface (iface
//     "<busid>:<bConfigurationValue>.0")
//  2. unbind the bare-device usb_driver (typically the generic "usb"
//     driver) so usbip-host can claim the usb_device
//  3. add the busID to usbip-host/match_busid
//  4. bind usbip-host to the device (BARE busid; usbip-host is a
//     usb_device_driver and matches at usb_device level)
//
// Step 4 failure rolls back step 3 (match_busid del) so the busid
// table is not poisoned. The earlier unbinds are preserved so the
// operator can rebind manually, matching upstream usbip_bind.c
// semantics.
//
// preflightBind() runs first: kernel-module check, vhci-loop refusal,
// hub guard (refuses bDeviceClass=0x09 to prevent cascade-disconnect),
// and already-exported short-circuit (returns ErrDeviceAlreadyBound
// before any sysfs mutation). The pipeline is serialized per-busid via
// lockBusID so concurrent Bind/Unbind calls cannot race on
// match_busid.
func (a *ExporterAdapter) Bind(ctx context.Context, busID domain.BusID) error {
	mu := a.lockBusID(busID)
	mu.Lock()
	defer mu.Unlock()

	err := a.preflightBind(ctx, busID)
	if err != nil {
		return err
	}

	// USB-to-Ethernet / cellular dongles register one or more netdevs
	// (cdc_ether, cdc_ncm, r8152, …). Those netdevs hold a
	// usb_device refcount as long as IFF_UP is set, so the bare
	// device unbind below would return EBUSY mid-cascade. Walk every
	// /net/<name> subtree (bare device + each interface) and clear
	// IFF_UP first.
	a.deactivateNetdevs(busID)

	// Bare-device unbind only. Mirrors upstream
	// usbip_bind.c::unbind_other() which detaches the usb_device
	// driver and lets USB core's generic disconnect cascade
	// (drivers/usb/core/generic.c:265-272) unbind every interface.
	// Doing the cascade ourselves with a hand-rolled iface unbind
	// diverges from upstream: it only covers <busid>:<cfg>.0 and
	// leaves composite devices in a half-state on subsequent
	// failure.
	err = a.unbindCurrentDeviceDriver(busID)
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

	// usbip-host is a usb_device_driver (drivers/usb/usbip/stub_dev.c
	// declares `struct usb_device_driver stub_driver`). The kernel's
	// driver bind/unbind sysfs handler looks up the target by name on
	// the bus and only proceeds when dev->driver matches. For a
	// device-level driver that means the BARE busid ("1-1"), not the
	// iface — passing iface here would find the usb_interface, fail
	// the dev->driver match, and surface ENODEV.
	err = a.bindUSBIPHostWithRetry(ctx, busID)
	if err != nil {
		a.rollbackBind(busID, err)

		return err
	}

	return nil
}

// rollbackBind undoes the match_busid add on a bind failure and
// nudges the kernel to re-attach the original device-level driver.
//
// Without the drivers_probe poke, the device is left orphaned after
// bind fails: bare-device unbind already detached the original driver
// and the kernel's auto-probe only fires on hot-plug events. Writing
// the busid to /sys/bus/usb/drivers_probe forces the kernel's driver
// match-table to re-evaluate immediately so udev/operators see a
// device with its original driver back. Both writes are best-effort —
// the primary bind error is the user-visible signal.
func (a *ExporterAdapter) rollbackBind(busID domain.BusID, primaryErr error) {
	rbErr := a.writeClassified(
		path.Join(SysfsUSBIPHostDriver, SysfsMatchBusID),
		MatchBusIDDelPrefix+string(busID),
	)
	if rbErr != nil {
		a.logger.Warn("bind rollback (match_busid del) failed",
			"busid", busID, "rollback_err", rbErr, "primary_err", primaryErr)
	}

	probeErr := a.writeClassified(SysfsDriversProbe, string(busID))
	if probeErr != nil {
		a.logger.Debug("bind rollback (drivers_probe) failed — kernel may auto-probe later",
			"busid", busID, "probe_err", probeErr, "primary_err", primaryErr)
	}
}

// checkAlreadyExported returns ErrDeviceAlreadyBound when the bare
// device is already bound to usbip-host. Runs BEFORE ifaceSuffix so
// that operators see the real already-bound state rather than a
// downstream "bConfigurationValue=0 (no active configuration)" error
// — that's the kernel state after USB core unconfigures the device on
// driver detach (drivers/usb/core/generic.c:265-272), and it persists
// for as long as usbip-host owns the device.
//
// Returns nil for "no driver attached" (transient hot-plug state) and
// for any non-usbip-host driver (typical case — the caller proceeds
// to detach it).
func (a *ExporterAdapter) checkAlreadyExported(busID domain.BusID) error {
	driver, err := a.currentDriver(string(busID))
	if errors.Is(err, domain.ErrDeviceNotBound) {
		return nil
	}

	if err != nil {
		return err
	}

	if driver == usbipHostDriverName {
		return fmt.Errorf("%w: device %s already bound to usbip-host", domain.ErrDeviceAlreadyBound, busID)
	}

	return nil
}

// unbindCurrentDeviceDriver releases the device-level usb_driver bound
// to the bare USB device (path /sys/bus/usb/devices/<busid>). usbip-host
// is a struct usb_device_driver and binds at usb_device level — the
// kernel's driver_match in bind_store rejects EBUSY when dev->driver is
// already set. Unless we actively detach the generic "usb" driver (or
// any other device-level driver) from the bare busid, the subsequent
// usbip-host/bind write returns EBUSY (surfaced as ErrDeviceAlreadyBound).
//
// Upstream usbip-utils tools/usb/usbip/src/usbip_bind.c::unbind_other()
// performs this same step before adding the busid to match_busid.
//
// Preconditions handled by the caller (Bind):
//   - checkAlreadyExported has already run, so any non-ENOENT
//     currentDriver error has already surfaced. Re-checking the
//     classification here would be dead code; instead, treat any
//     read failure as "no driver to unbind" and continue. The kernel
//     will surface a real EBUSY at the bind step if the bare device
//     still owns a driver.
func (a *ExporterAdapter) unbindCurrentDeviceDriver(busID domain.BusID) error {
	driver, err := a.currentDriver(string(busID))
	if err != nil {
		a.logger.Debug("bind: skipping device-level unbind — driver state unreadable or absent",
			"busid", busID, "err", err)

		return nil
	}

	return a.writeClassified(driverPath(driver, SysfsDriverUnbind), string(busID))
}

// preflightUnbind runs non-mutating checks before Unbind touches
// sysfs: kernel module availability and the driver==usbip-host
// precheck. Extracted to keep Unbind() under gocognit threshold.
func (a *ExporterAdapter) preflightUnbind(ctx context.Context, busID domain.BusID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	// Refuse if the device is not actually bound to usbip-host.
	// Upstream usbip_unbind.c::unbind_device() does the same precheck
	// at lines 54-58. Without it we'd write match_busid del /
	// usbip-host/unbind for busids we never claimed.
	driver, err := a.currentDriver(string(busID))
	if errors.Is(err, domain.ErrDeviceNotBound) {
		return fmt.Errorf("%w: device %s has no driver attached", domain.ErrDeviceNotBound, busID)
	}

	if err != nil {
		return err
	}

	if driver != usbipHostDriverName {
		return fmt.Errorf("%w: device %s is bound to %q, not usbip-host",
			domain.ErrDeviceNotBound, busID, driver)
	}

	return nil
}

// Unbind reverses Bind: refuses if the device is not bound to
// usbip-host (precheck mirrors upstream usbip_unbind.c lines 54-58),
// gracefully disconnects any active importer session, writes the
// usbip-host/unbind sequence, removes the busID from match_busid,
// and triggers a default-driver rebind.
//
// The pre-disconnect step is crucial: writing -1 to the per-device
// usbip_sockfd attribute triggers SDEV_EVENT_DOWN in the kernel and
// drops any in-flight URBs cleanly. Without it, the subsequent
// usbip-host/unbind write blocks indefinitely while the kernel waits
// for the importer socket to drain — operators saw `usbip-go unbind`
// hang. The pre-disconnect failure is non-fatal: a freshly-bound
// device with no active session has no sockfd attribute (or the
// attribute returns ENODEV); the unbind sequence continues either
// way. Serialized per-busid via lockBusID.
func (a *ExporterAdapter) Unbind(ctx context.Context, busID domain.BusID) error {
	mu := a.lockBusID(busID)
	mu.Lock()
	defer mu.Unlock()

	err := a.preflightUnbind(ctx, busID)
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

	// match_busid del runs regardless of the unbind result so the
	// device table is not stuck with a stale entry. The rebind
	// trigger is gated on unbind success: kernels through 6.8 (Pi 5)
	// NULL-deref in do_rebind() when the per-busid stub has no live
	// udev — exactly the state when unbind failed (no stub_probe
	// ever ran, or the device was already detached). Writing to
	// rebind in that state oopses the kernel. The C usbip-utils only
	// triggers rebind after a successful unbind for the same reason.
	matchErr := a.writeClassified(
		path.Join(SysfsUSBIPHostDriver, SysfsMatchBusID),
		MatchBusIDDelPrefix+string(busID),
	)

	var rebindErr error
	if unbindErr == nil {
		rebindErr = a.writeClassified(path.Join(SysfsUSBIPHostDriver, SysfsRebind), string(busID))
	}

	switch {
	case unbindErr != nil:
		if matchErr != nil {
			a.logger.Warn("unbind: match_busid del failed after primary unbind error",
				"busid", busID, "match_err", matchErr, "primary_err", unbindErr)
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
				return fmt.Errorf("bind: retry interrupted: %w", ctx.Err())
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
//
// Composite CDC devices (cdc_ncm + cdc_ether on the same hardware,
// e.g. ASIX AX88179) register the netdev under the *interface* node
// (/sys/bus/usb/devices/<busid>:<cfg>.<iface>/net/<name>) rather than
// directly under the bare device. Walk both the bare-device /net
// subdir and every interface /net subdir so the IFF_UP clear hits
// regardless of where the kernel attached the netdev.
func (a *ExporterAdapter) deactivateNetdevs(busID domain.BusID) {
	netdevs := a.collectNetdevNames(busID)
	if len(netdevs) == 0 {
		return
	}

	for _, name := range netdevs {
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

// collectNetdevNames enumerates every netdev associated with busID by
// scanning two sysfs locations:
//
//  1. /sys/bus/usb/devices/<busID>/net/* — the simple case where the
//     device-level driver owns the netdev directly.
//  2. /sys/bus/usb/devices/<busID>:*/net/* — composite devices that
//     register the netdev under an interface (cdc_ncm, cdc_ether on
//     ASIX AX88179, etc.). Without this, deactivateNetdevs misses
//     netdevs entirely on multi-interface USB ethernet adapters.
//
// Returns deduplicated names; the same netdev may appear in both
// locations if sysfs cross-links the bare device into the interface
// dir on some kernel versions.
func (a *ExporterAdapter) collectNetdevNames(busID domain.BusID) []string {
	seen := map[string]struct{}{}

	a.addNetdevsFrom(seen, path.Join(SysfsUSBDevices, string(busID), "net"))

	for _, ifaceDir := range a.busIDInterfaceDirs(busID) {
		a.addNetdevsFrom(seen, path.Join(SysfsUSBDevices, ifaceDir, "net"))
	}

	if len(seen) == 0 {
		return nil
	}

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}

	return out
}

// addNetdevsFrom appends every entry under netRoot into seen. Missing
// directories are ignored (most USB devices have no /net subtree at all).
func (a *ExporterAdapter) addNetdevsFrom(seen map[string]struct{}, netRoot string) {
	entries, err := ListDirEntries(a.fs, netRoot)
	if err != nil {
		return
	}

	for _, name := range entries {
		seen[name] = struct{}{}
	}
}

// busIDInterfaceDirs returns the names of every sysfs entry under
// /sys/bus/usb/devices that belongs to busID's interface set
// (e.g. "3-1:1.0", "3-1:2.0", "3-1:2.1"). Returns nil on read error.
func (a *ExporterAdapter) busIDInterfaceDirs(busID domain.BusID) []string {
	entries, err := ListDirEntries(a.fs, SysfsUSBDevices)
	if err != nil {
		return nil
	}

	prefix := string(busID) + ":"
	out := make([]string, 0)

	for _, e := range entries {
		if strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}

	return out
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

// usbHubClass is the bDeviceClass value the USB spec assigns to hub
// devices. usbip-host's stub_probe rejects hubs in
// drivers/usb/usbip/stub_dev.c (lines ~347-351) but only AFTER the
// generic-usb driver has already been detached, which by that point
// has cascade-disconnected every downstream device. Refusing at the
// adapter layer prevents that destructive prelude.
const usbHubClass = 0x09

// refuseHubDevice rejects bind attempts against USB hub devices
// before any sysfs mutation. Reads /sys/bus/usb/devices/<busID>/
// bDeviceClass and returns ErrUnsupportedDevice when the value is
// 0x09 (HUB). Mirrors upstream usbip_bind.c::unbind_other() lines
// 82-91 which performs the same check userspace-side.
//
// A missing bDeviceClass attribute is treated as "no — let the
// downstream sysfs reads surface ErrDeviceNotFound" rather than
// asserting hub-ness; the guard is best-effort defense, not the
// device-existence check.
func (a *ExporterAdapter) refuseHubDevice(busID domain.BusID) error {
	classRaw, err := ReadHex16(a.fs, path.Join(SysfsUSBDevices, string(busID), devAttrDeviceClass))
	if err != nil {
		// Missing attribute (real ENOENT or wrapped ErrDeviceNotFound):
		// let downstream readers raise the canonical not-found error.
		if isMissing(err) {
			return nil
		}

		return fmt.Errorf("hub guard: read bDeviceClass %s: %w", busID, err)
	}

	if classRaw == usbHubClass {
		return fmt.Errorf("%w: %s is a USB hub (bDeviceClass=0x09); detaching its driver "+
			"would disconnect every downstream device", domain.ErrUnsupportedDevice, busID)
	}

	return nil
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
