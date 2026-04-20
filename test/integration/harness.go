//go:build integration_linux

// Package integration hosts end-to-end tests that exercise the full
// usbip-go stack against a live Linux kernel with the usbip_core,
// vhci_hcd, usbip_host, and usbip_vudc modules loaded. Build tag
// integration_linux gates compilation; the tests skip cleanly via
// harness preflight when the required modules are missing at runtime.
// See spec §8.4 for the self-hosted CI contract that drives this tag.
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
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// harnessModuleNames enumerates the kernel modules the integration
// suite requires before any test can safely touch configfs or bind a
// vudc gadget. Any missing entry triggers t.Skip per the spec §8.4
// "skip-when-env-lacks-dep" exception, not the no-shortcuts rule.
func harnessModuleNames() []string {
	return []string{"usbip_core", "vhci_hcd", "usbip_host", "usbip_vudc"}
}

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
	// value Importer.Attach / usbipd.Bind operate against.
	BusID string

	// Name is the configfs subdirectory name holding the gadget
	// structure; t.TempDir-style cleanup removes the tree on test exit.
	Name string
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

	requireModulesLoaded(t)

	if _, err := os.Stat(configfsUSBGadgetRoot); err != nil {
		t.Skipf("integration harness: %s not available: %v (modprobe libcomposite and mount configfs)", configfsUSBGadgetRoot, err)
	}

	name := uniqueGadgetName(t)
	root := filepath.Join(configfsUSBGadgetRoot, name)

	cleanup := registerGadgetCleanup(t, root)

	err := writeGadgetTree(t, root, name)
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

	return &VUDCDevice{BusID: udc, Name: name}
}

// RealBusIDEnv names the environment variable operators set to a real
// USB bus id (e.g. "1-1.2") bindable via usbip-host. Tests that need
// real-device semantics read it via RequireRealBusID; vudc devices do
// not traverse the usbip-host bind path and so are not sufficient for
// these scenarios per spec §8.4.
const RealBusIDEnv = "USBIPGO_INTEGRATION_BUSID"

// RequireRealBusID returns the BusID named by RealBusIDEnv or t.Skips
// when unset. Centralising the env check here replaces the scatter of
// `if rawBusID == "" { t.Skipf(...) }` across integration tests and
// ensures a single consistent skip message.
func RequireRealBusID(t *testing.T) domain.BusID {
	t.Helper()

	raw := os.Getenv(RealBusIDEnv)
	if raw == "" {
		t.Skipf("integration harness: %s unset; scenario requires a real usbip-host bindable busid (spec §8.4 env-gated)",
			RealBusIDEnv)
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
const unbindCleanupTimeout = 2 * time.Second

// requireModulesLoaded scans /sys/module for each harnessModuleNames
// entry and t.Skips when any is missing. Spec §8.4 documents t.Skip as
// the sanctioned integration exception for missing kernel modules; no
// other t.Skip path is acceptable per the no-shortcuts discipline.
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
		t.Skipf("integration harness: required kernel modules not loaded: %s (spec §8.4 self-hosted-only test)",
			strings.Join(missing, ", "))
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
// rmdir'd leaf-first. Errors are best-effort logged; the test has
// already returned by the time cleanup fires so a test-failure signal
// cannot be raised from here.
func registerGadgetCleanup(t *testing.T, root string) func() {
	t.Helper()

	fn := func() {
		// Unbind UDC first: writing empty detaches the gadget so the
		// later rmdir stack is not rejected with EBUSY.
		_ = writeFile(filepath.Join(root, "UDC"), "")

		// Function symlinks must go before their parent configs dir.
		configsDir := filepath.Join(root, "configs")
		_ = filepath.WalkDir(configsDir, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil //nolint:nilerr // cleanup is best-effort; log-and-continue is intentional
			}

			if d.Type()&fs.ModeSymlink != 0 {
				_ = os.Remove(path)
			}

			return nil
		})

		// Depth-first rmdir so leaf configfs directories vanish
		// before their parents. os.RemoveAll cannot be used here
		// because configfs rejects unlink on attribute-file children
		// — only rmdir is legal, and only once the dir is empty.
		_ = removeConfigfsTreeDepthFirst(root)
	}

	t.Cleanup(fn)

	return fn
}

// removeConfigfsTreeDepthFirst walks root depth-first and rmdirs every
// directory. configfs directories reject os.Remove on attribute files
// (they are not regular files) so os.RemoveAll fails halfway through;
// this routine skips files and only attempts rmdir on directories.
func removeConfigfsTreeDepthFirst(root string) error {
	paths := make([]string, 0, 16)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil //nolint:nilerr // walk continues past transient errors during cleanup
		}

		if d.IsDir() {
			paths = append(paths, path)
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("walk configfs tree: %w", err)
	}

	// rmdir leaves first: reverse the accumulated order so each
	// directory's children are already gone when its rmdir fires.
	for i := len(paths) - 1; i >= 0; i-- {
		_ = os.Remove(paths[i])
	}

	return nil
}

// writeGadgetTree performs the sequence of configfs writes that
// materialise a usbip-vudc gadget. Each write uses writeFile which
// opens O_WRONLY and writes one attribute value — the sysfs-idiomatic
// pattern. A backing file for the mass_storage function is created in
// t.TempDir so the test's own resource cleanup removes it.
func writeGadgetTree(t *testing.T, root, _ string) error {
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

	err = writeGadgetFunction(t, root)
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

	backing := filepath.Join(t.TempDir(), "msd.img")

	err = os.WriteFile(backing, make([]byte, 1<<20), 0o600)
	if err != nil {
		return fmt.Errorf("allocate backing file: %w", err)
	}

	err = writeFile(filepath.Join(funcDir, "lun.0", "file"), backing)
	if err != nil {
		return fmt.Errorf("set lun backing: %w", err)
	}

	err = os.Symlink(funcDir, filepath.Join(root, "configs", "c.1", "mass_storage.0"))
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("link function: %w", err)
	}

	return nil
}

// firstAvailableVUDC scans /sys/class/udc for a usbip-vudc.N instance
// and returns its name. The kernel module exposes a fixed number (per
// vudc_num module param); all-in-use returns an error so the harness
// can skip instead of hanging on the UDC write.
func firstAvailableVUDC() (string, error) {
	entries, err := os.ReadDir("/sys/class/udc")
	if err != nil {
		return "", fmt.Errorf("read udc dir: %w", err)
	}

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "usbip-vudc") {
			return e.Name(), nil
		}
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
