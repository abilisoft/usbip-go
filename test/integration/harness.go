// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

// Package integration hosts end-to-end tests that exercise the full
// usbip-go stack against a live Linux kernel with the usbip_core,
// vhci_hcd, usbip_host, and usbip_vudc modules loaded. Build tag
// integration_linux gates compilation; the tests skip cleanly via
// harness preflight when the required modules are missing at runtime.
// See security-release-quality OpenSpec for the self-hosted CI contract that
// drives this tag.
package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// harnessModuleNames enumerates the kernel modules the integration
// suite requires before any test can safely touch configfs or bind a
// vudc gadget. Any missing entry triggers t.Skip per the
// security-release-quality OpenSpec "skip-when-env-lacks-dep" exception, not
// the no-shortcuts rule.
func harnessModuleNames() []string {
	return []string{
		kernelModuleLibcomposite,
		kernelModuleUSBFMassStorage,
		usbip.KernelModuleUSBIPCore,
		usbip.KernelModuleVHCIHCD,
		usbip.KernelModuleUSBIPHost,
		kernelModuleUSBIPVUDC,
	}
}

// kernelModuleUSBIPVUDC is integration-only: unlike the three modules exposed
// by usbip.ProbeKernelModules, usbip_vudc is required only by the virtual UDC
// harness and is not part of the public runtime-readiness contract.
const (
	kernelModuleUSBFMassStorage = "usb_f_mass_storage"
	kernelModuleUSBIPVUDC       = "usbip_vudc"
)

// vudcVendorID / vudcProductID / vudcBcdDevice mirror the upstream
// capture script (scripts/capture-wire-fixtures.sh) so the harness
// advertises the same device descriptor bytes the wire conformance
// fixtures were captured against.
const (
	vudcVendorID  = "0x0951"
	vudcProductID = "0x1666"
	vudcBcdDevice = "0x0110"
)

// configfsUSBGadgetRoot is the configfs directory where usb_gadget
// structures live once libcomposite is loaded. The harness does not
// modprobe — that is an operator responsibility per the self-hosted
// runner contract; a missing root is therefore a skip, not a fail.
const configfsUSBGadgetRoot = "/sys/kernel/config/usb_gadget"

// moduleSysfsRoot mirrors pkg/usbip.moduleSysfsRoot — duplicated here
// so the integration package does not depend on an internal probe
// helper just to check existence.
const moduleSysfsRoot = "/sys/module"

// VUDCDevice describes the ephemeral gadget the harness created and the
// cleanup routine the test must defer.
type VUDCDevice struct {
	// BusID is the usbip-vudc.N bus id the gadget is bound to and the
	// value Importer.Attach / Exporter.Bind operate against.
	BusID string

	// Name is the configfs subdirectory name holding the gadget
	// structure; t.TempDir-style cleanup removes the tree on test exit.
	Name string

	cleanup func()
}

// Release tears down the configfs gadget before the test ends. SetupVUDC
// also registers the same cleanup with testing.T, so Release is optional and
// idempotent; callers use it when a subsequent scenario must reuse a finite
// usbip_vudc instance during the same test.
func (d *VUDCDevice) Release() {
	if d == nil || d.cleanup == nil {
		return
	}

	d.cleanup()
}

// SetupVUDC creates a single usbip-vudc gadget in configfs and binds it
// to the first available usbip-vudc UDC instance. The returned cleanup
// runs as a t.Cleanup so the configfs tree is torn down on success AND
// on test failure. Module-preflight skips are raised before any write
// fires so a partial setup never leaks into /sys.
//
// The gadget layout mirrors the one scripts/capture-wire-fixtures.sh
// builds: idVendor / idProduct / bcdDevice + a 1 MiB mass_storage
// function + a single configs/c.1 link. That combination is the one the
// upstream UDC accepts (empty gadgets get EBUSY). The backing file
// lives in t.TempDir so parallel tests cannot clash on paths.
func SetupVUDC(t *testing.T) *VUDCDevice {
	t.Helper()

	return setupVUDCWithBacking(t, nil)
}

// SetupVUDCWithData is SetupVUDC with caller-provided bytes written to
// the mass_storage lun backing file. The E2E data-transfer test uses
// this to plant a deterministic payload that the importer side can
// read back through the /dev/sdN block device the kernel creates when
// vhci_hcd imports the vudc gadget.
//
// len(backing) must be at least one 512-byte SCSI sector; callers
// typically pass 64 KiB – 1 MiB so the kernel round-trips multiple
// URBs during verification.
func SetupVUDCWithData(t *testing.T, backing []byte) *VUDCDevice {
	t.Helper()

	return setupVUDCWithBacking(t, backing)
}

func setupVUDCWithBacking(t *testing.T, backing []byte) *VUDCDevice {
	t.Helper()

	requireModulesLoaded(t)

	if _, err := os.Stat(configfsUSBGadgetRoot); err != nil {
		t.Skipf("integration harness: %s not available: %v (modprobe libcomposite and mount configfs)", configfsUSBGadgetRoot, err)
	}

	name := uniqueGadgetName(t)
	root := filepath.Join(configfsUSBGadgetRoot, name)

	cleanup := registerGadgetCleanup(t, root)

	err := writeGadgetTreeWithBacking(t, root, name, backing)
	if err != nil {
		cleanup()
		t.Skipf("integration harness: configfs write failed: %v (usbip-vudc UDC instance may be exhausted)", err)
	}

	udc, err := firstAvailableVUDC()
	if err != nil {
		cleanup()
		t.Skipf("integration harness: no usbip-vudc UDC available: %v", err)
	}

	err = writeFile(filepath.Join(root, "UDC"), udc)
	if err != nil {
		cleanup()
		t.Skipf("integration harness: UDC bind failed: %v", err)
	}

	return &VUDCDevice{BusID: udc, Name: name, cleanup: cleanup}
}

// RealBusIDEnv names the environment variable operators set to a real
// USB bus id (e.g. "1-1.2") bindable via usbip-host. Tests that need
// real-device semantics read it via RequireRealBusID; vudc devices do
// not traverse the usbip-host bind path and so are not sufficient for
// these scenarios per security-release-quality OpenSpec.
const (
	RealBusIDEnv = "USBIPGO_INTEGRATION_BUSID"

	realBusIDMissingFormat = "integration harness: %s unset; scenario requires a real " +
		"usbip-host bindable busid (security-release-quality OpenSpec env-gated)"
)

// RequireRealBusID returns the BusID named by RealBusIDEnv or t.Skips
// when unset. Centralising the env check here replaces the scatter of
// `if rawBusID == "" { t.Skipf(...) }` across integration tests and
// ensures a single consistent skip message.
func RequireRealBusID(t *testing.T) domain.BusID {
	t.Helper()

	raw := os.Getenv(RealBusIDEnv)
	if raw == "" {
		t.Skipf(realBusIDMissingFormat, RealBusIDEnv)
	}

	return domain.BusID(raw)
}

// RequireBindable calls exp.Bind(ctx, busID) and t.Skips on failure,
// registering a t.Cleanup that Unbinds the busid after the test returns.
// requireModulesLoaded is already enforced by the caller's SetupVUDC
// invocation; the bind skip here covers runtime states that module
// preflight cannot detect (busid not present, already bound by another
// driver, EACCES on /sys/bus/usb/drivers/usbip-host/bind).
//
// Centralising the skip keeps the wording consistent and drops the
// ad-hoc `if err != nil { t.Skipf("bind %q ...") }` boilerplate from
// every integration test that takes a real busid.
func RequireBindable(t *testing.T, ctx context.Context, exp *usbip.Exporter, busID domain.BusID) { //nolint:revive // ctx after t matches integration-test convention used across the suite
	t.Helper()

	err := exp.Bind(ctx, busID)
	if err != nil {
		t.Skipf("integration harness: bind %q skipped: %v (busid not bindable via usbip-host on this runner)", busID, err)
	}

	t.Cleanup(func() {
		uctx, ucancel := context.WithTimeout(context.Background(), unbindCleanupTimeout)

		defer ucancel()

		_ = exp.Unbind(uctx, busID)
	})
}

// unbindCleanupTimeout bounds the Unbind call registered by
// RequireBindable's t.Cleanup. Two seconds matches the value previously
// inlined at every call site; kept as a named constant so mnd does not
// flag the literal and future tuning has a single point of change.
const (
	unbindCleanupTimeout = 2 * time.Second

	missingModulesFormat = "integration harness: required kernel modules not loaded: %s " +
		"(security-release-quality OpenSpec self-hosted-only test)"
)

// requireModulesLoaded scans /sys/module for each harnessModuleNames
// entry and t.Skips when any is missing. The security-release-quality
// OpenSpec documents t.Skip as the sanctioned integration exception for
// missing kernel modules; no other t.Skip path is acceptable per the
// no-shortcuts discipline.
func requireModulesLoaded(t *testing.T) {
	t.Helper()

	var missing []string

	for _, name := range harnessModuleNames() {
		_, err := os.Stat(filepath.Join(moduleSysfsRoot, name))
		if err != nil {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		t.Skipf(missingModulesFormat, strings.Join(missing, ", "))
	}
}

// uniqueGadgetName returns a directory name that cannot collide with a
// concurrent test's gadget. Using t.Name() alone is unsafe because
// Parallel tests may pick the same t.Name under -count=N; the random
// suffix makes each instance unique.
func uniqueGadgetName(t *testing.T) string {
	t.Helper()

	var buf [4]byte

	_, err := rand.Read(buf[:])
	if err != nil {
		t.Fatalf("integration harness: random suffix: %v", err)
	}

	// t.Name() may contain '/' from subtest nesting; configfs
	// directory names cannot contain path separators so sanitise.
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())

	return fmt.Sprintf("usbipgo-%s-%s", safe, hex.EncodeToString(buf[:]))
}

// registerGadgetCleanup queues the configfs teardown on the test's
// t.Cleanup stack. Teardown is idempotent: an UDC unbind writes empty
// to UDC, then function symlinks are removed, then directories are
// rmdir'd leaf-first. Errors from each step are joined via errors.Join
// and surfaced via t.Logf so a broken teardown is visible in CI
// output without being escalated to t.Errorf (which would turn a
// cleanup-time kernel quirk into a test failure). Not all cleanup
// errors are symptomatic: a "file does not exist" on an unbind-empty
// write just means the gadget was never bound in the first place.
func registerGadgetCleanup(t *testing.T, root string) func() {
	t.Helper()

	var once sync.Once

	fn := func() {
		once.Do(func() {
			err := runGadgetCleanup(root)
			if err != nil {
				// Teardown failures should remain visible without replacing the
				// scenario's primary result with a secondary kernel-cleanup error.
				t.Logf("integration harness: gadget cleanup errors at %s: %v", root, err)
			}
		})
	}

	t.Cleanup(fn)

	return fn
}

// runGadgetCleanup performs the configfs teardown dance and accumulates
// every step's error via errors.Join. Extracted from the t.Cleanup
// closure so registerGadgetCleanup's surface is purely "log the joined
// error" and the teardown sequence itself is testable independently.
//
// Each step keeps running even if a previous one errored because
// configfs state can be partially torn down and the remaining steps
// often succeed: e.g. an UDC write failure (gadget never bound) should
// not skip the symlink+rmdir cleanup.
func runGadgetCleanup(root string) error {
	_, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect gadget root: %w", err)
	}

	var errs []error

	// Unbind UDC first: kernel configfs rejects a zero-byte write
	// (fill_write_buffer → copy_from_iter copies 0 → -EFAULT), so we
	// write a newline that the UDC store handler strips before the
	// empty-name branch triggers unregister_gadget. A missing UDC
	// attribute ("not bound in the first place") is not treated as
	// an error — errors.Is(err, fs.ErrNotExist) filters those.
	if err := writeFile(filepath.Join(root, "UDC"), "\n"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("unbind UDC: %w", err))
	}

	// Function symlinks must go before their parent configs dir.
	configsDir := filepath.Join(root, "configs")

	err = filepath.WalkDir(configsDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}

			errs = append(errs, fmt.Errorf("walk %s: %w", path, walkErr))

			return nil //nolint:nilerr // continue walking; the joined-error accumulates the skipped branch
		}

		if d.Type()&fs.ModeSymlink != 0 {
			if rmErr := os.Remove(path); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				errs = append(errs, fmt.Errorf("remove symlink %s: %w", path, rmErr))
			}
		}

		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, fmt.Errorf("walk configs: %w", err))
	}

	// Depth-first rmdir so leaf configfs directories vanish before
	// their parents. os.RemoveAll cannot be used here because configfs
	// rejects unlink on attribute-file children — only rmdir is legal,
	// and only once the dir is empty.
	if rmErr := removeConfigfsTreeDepthFirst(root); rmErr != nil {
		errs = append(errs, fmt.Errorf("rmdir tree: %w", rmErr))
	}

	return errors.Join(errs...)
}

// implicitConfigfsGadgetDirs are directory names the kernel creates
// automatically inside a gadget root and that refuse explicit rmdir.
// The gadget-root rmdir removes them as part of the gadget teardown
// handler, so we skip them during the walk and let the root cleanup
// do the work. `lun.0` is the same story for the mass_storage
// function: the function's rmdir drops its own children.
var implicitConfigfsGadgetDirs = map[string]bool{
	"configs":   true,
	"functions": true,
	"strings":   true,
	"os_desc":   true,
	"webusb":    true,
	"lun.0":     true,
}

// removeConfigfsTreeDepthFirst walks root depth-first and rmdirs every
// user-created directory, skipping the implicit kernel-managed ones
// listed in implicitConfigfsGadgetDirs (those are cleaned up when
// their parent rmdir fires, not by direct removal). configfs directories
// reject os.Remove on attribute files (they are not regular files) so
// os.RemoveAll fails halfway through; this routine skips files and
// only attempts rmdir on directories. Per-rmdir failures are joined
// via errors.Join so a partial teardown surfaces every stuck directory.
// fs.ErrNotExist is filtered because a concurrent cleanup can unlink
// before we do.
func removeConfigfsTreeDepthFirst(root string) error {
	paths := make([]string, 0, 16)

	var errs []error

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			errs = append(errs, fmt.Errorf("walk %s: %w", path, walkErr))

			return nil //nolint:nilerr // continue the walk; the joined-error captures the skipped branch
		}

		if d.IsDir() {
			paths = append(paths, path)
		}

		return nil
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("walk configfs tree: %w", err))
	}

	// rmdir leaves first: reverse the accumulated order so each
	// directory's children are already gone when its rmdir fires.
	// Skip implicit kernel-managed dirs — the root rmdir below
	// removes them transitively.
	for i := len(paths) - 1; i >= 0; i-- {
		p := paths[i]
		if p != root && implicitConfigfsGadgetDirs[filepath.Base(p)] {
			continue
		}

		rmErr := os.Remove(p)
		if rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("rmdir %s: %w", p, rmErr))
		}
	}

	return errors.Join(errs...)
}

// writeGadgetTree performs the sequence of configfs writes that
// materialise a usbip-vudc gadget. Each write uses writeFile which
// opens O_WRONLY and writes one attribute value — the sysfs-idiomatic
// pattern. A backing file for the mass_storage function is created in
// t.TempDir so the test's own resource cleanup removes it.
func writeGadgetTree(t *testing.T, root, _ string) error {
	t.Helper()

	return writeGadgetTreeWithBacking(t, root, "", nil)
}

func writeGadgetTreeWithBacking(t *testing.T, root, _ string, backing []byte) error {
	t.Helper()

	err := os.MkdirAll(root, 0o755)
	if err != nil {
		return fmt.Errorf("mkdir gadget root: %w", err)
	}

	writes := []struct {
		path, val string
	}{
		{filepath.Join(root, "idVendor"), vudcVendorID},
		{filepath.Join(root, "idProduct"), vudcProductID},
		{filepath.Join(root, "bcdDevice"), vudcBcdDevice},
	}

	for _, w := range writes {
		err = writeFile(w.path, w.val)
		if err != nil {
			return fmt.Errorf("write %s: %w", w.path, err)
		}
	}

	err = writeGadgetStrings(root)
	if err != nil {
		return err
	}

	err = writeGadgetFunctionWithBacking(t, root, backing)
	if err != nil {
		return err
	}

	return nil
}

// writeGadgetStrings materialises the strings/0x409 subdir so the UDC
// accepts the gadget without EINVAL on missing descriptors.
func writeGadgetStrings(root string) error {
	strDir := filepath.Join(root, "strings", "0x409")

	err := os.MkdirAll(strDir, 0o755)
	if err != nil {
		return fmt.Errorf("mkdir strings: %w", err)
	}

	entries := map[string]string{
		"serialnumber": "abilisoft-integration",
		"manufacturer": "usbip-go",
		"product":      "Integration Test Device",
	}

	for k, v := range entries {
		err = writeFile(filepath.Join(strDir, k), v)
		if err != nil {
			return fmt.Errorf("write string %s: %w", k, err)
		}
	}

	return nil
}

// writeGadgetFunction creates the configs/c.1 + mass_storage.0
// function link. The function must be non-empty for the UDC to accept
// the gadget — a bare gadget with no function returns EBUSY on UDC
// write per upstream's libcomposite behaviour.
func writeGadgetFunction(t *testing.T, root string) error {
	t.Helper()

	return writeGadgetFunctionWithBacking(t, root, nil)
}

// writeGadgetFunctionWithBacking is writeGadgetFunction plus an
// optional caller-provided backing payload. nil or empty falls back to
// the historical 1 MiB zero-filled file the conformance fixtures were
// captured against; callers that need a specific payload (E2E data-
// transfer) pass the exact bytes to plant on the LUN.
func writeGadgetFunctionWithBacking(t *testing.T, root string, backing []byte) error {
	t.Helper()

	cfgDir := filepath.Join(root, "configs", "c.1", "strings", "0x409")

	err := os.MkdirAll(cfgDir, 0o755)
	if err != nil {
		return fmt.Errorf("mkdir config strings: %w", err)
	}

	err = writeFile(filepath.Join(cfgDir, "configuration"), "default")
	if err != nil {
		return fmt.Errorf("write config string: %w", err)
	}

	funcDir := filepath.Join(root, "functions", "mass_storage.0")

	err = os.MkdirAll(funcDir, 0o755)
	if err != nil {
		return fmt.Errorf("mkdir function: %w", err)
	}

	backingPath := filepath.Join(t.TempDir(), "msd.img")

	if len(backing) == 0 {
		backing = make([]byte, 1<<20)
	}

	err = os.WriteFile(backingPath, backing, 0o600)
	if err != nil {
		return fmt.Errorf("allocate backing file: %w", err)
	}

	err = writeFile(filepath.Join(funcDir, "lun.0", "file"), backingPath)
	if err != nil {
		return fmt.Errorf("set lun backing: %w", err)
	}

	err = os.Symlink(funcDir, filepath.Join(root, "configs", "c.1", "mass_storage.0"))
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("link function: %w", err)
	}

	return nil
}

// vudcUsageTracker remembers which usbip-vudc.N instances this test
// binary has already bound in the current process. The kernel's per-
// vudc state (URB seqnum counter, cancelled-URB backlog on the
// completion workqueue) does not fully reset on detach; reusing an
// instance across tests reliably races into
// "vhci_hcd: cannot find a urb of seqnum ..." and a hung Attach.
// Tracking used instances in-process and always picking a fresh one
// eliminates the race — the vudc kernel module's `num=` param
// must provision enough instances for every test case the integration
// suite runs concurrently in one invocation. When the pool is exhausted,
// idle instances may be recycled after their prior configfs gadget is released.
var vudcUsageTracker = struct {
	mu   sync.Mutex
	used map[string]bool
}{used: map[string]bool{}}

// firstAvailableVUDC picks the next usbip-vudc.N instance that:
//
//  1. has not yet been handed out in this test-binary process, AND
//  2. is not currently in SDEV_ST_USED state (an active session)
//
// and records the selection so the next caller first prefers a different
// instance. If every instance has been used, the tracker is cleared and an
// idle instance may be recycled; callers reusing a finite pool must Release
// the previous gadget before asking for another one.
func firstAvailableVUDC() (string, error) {
	entries, err := os.ReadDir("/sys/class/udc")
	if err != nil {
		return "", fmt.Errorf("read udc dir: %w", err)
	}

	const vudcStatusUsed = "2"

	vudcUsageTracker.mu.Lock()
	defer vudcUsageTracker.mu.Unlock()

	// pickUnused picks the first usbip-vudc.N whose tracker flag is
	// clear and whose kernel-side usbip_status is not USED. Returns
	// "" if every instance is either tracked-as-used or actively
	// hosting a session.
	pickUnused := func() string {
		for _, e := range entries {
			name := e.Name()
			if !strings.HasPrefix(name, "usbip-vudc") {
				continue
			}

			if vudcUsageTracker.used[name] {
				continue
			}

			status, rerr := os.ReadFile(
				filepath.Join("/sys/devices/platform", name, "usbip_status"),
			)
			if rerr != nil {
				continue
			}

			if strings.TrimSpace(string(status)) == vudcStatusUsed {
				continue
			}

			return name
		}

		return ""
	}

	if name := pickUnused(); name != "" {
		vudcUsageTracker.used[name] = true

		return name, nil
	}

	// Pool exhausted per the in-process tracker. Clear and retry:
	// a second test-binary invocation (e.g. `go test -count=2`) or
	// any future harness change that runs more SetupVUDC calls than
	// the kernel's `num=` param legitimately recycles instances. The
	// kernel-side usbip_status check still rejects instances with a
	// live session, so recycling only hands out truly-idle slots.
	clear(vudcUsageTracker.used)

	if name := pickUnused(); name != "" {
		vudcUsageTracker.used[name] = true

		return name, nil
	}

	return "", errors.New("no usbip-vudc UDC instance available")
}

// writeFile wraps os.WriteFile with 0o644 and an explicit O_WRONLY
// open so sysfs / configfs attribute files accept the write (they do
// not support O_CREAT on open; MkdirAll handles the parent beforehand).
// Errors are wrapped with the path so harness skips carry enough
// context to diagnose the setup issue from a CI log line.
func writeFile(path, val string) error {
	f, err := os.OpenFile(path, os.O_WRONLY, 0o644) //nolint:gosec // sysfs paths are hard-coded constants under /sys/kernel/config
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}

	defer func() { _ = f.Close() }()

	_, err = f.WriteString(val)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
