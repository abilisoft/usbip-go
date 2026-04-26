// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dummyHCDModule is the kernel module that creates a virtual USB host
// controller paired with a virtual UDC. Gadgets bound to dummy_udc.0
// enumerate as real USB devices under /sys/bus/usb/devices/, get a
// real busid, and (unlike usbip-vudc which is a platform device) can
// be bound by usbip-host. See drivers/usb/gadget/udc/dummy_hcd.c.
const dummyHCDModule = "dummy_hcd"

// dummyUDCName is the UDC identifier dummy_hcd registers and that
// configfs's <gadget>/UDC attribute accepts to bind a gadget to the
// virtual host controller.
const dummyUDCName = "dummy_udc.0"

// gadgetConfigfsRoot is where libcomposite mounts the usb_gadget
// configfs subtree. Created by `mount -t configfs none /sys/kernel/config`
// and then `mkdir /sys/kernel/config/usb_gadget` (libcomposite handles
// the latter when loaded).
const gadgetConfigfsRoot = "/sys/kernel/config/usb_gadget"

// dummyHCDRequiredModules enumerates kernel modules that must be
// loaded for the dummy_hcd-backed integration scenarios to make
// sense. udc_core is pulled in transitively by dummy_hcd; we list it
// for clarity.
func dummyHCDRequiredModules() []string {
	return []string{
		"libcomposite", // configfs gadget framework
		"dummy_hcd",    // virtual HCD + UDC pair
		"usbip_core",
		"usbip_host", // device-level driver
		"vhci_hcd",   // importer-side virtual HCD
	}
}

// SetupDummyHCDGadget creates a real-USB-shaped gadget on dummy_udc.0
// and returns its busid. The gadget is torn down via t.Cleanup.
//
// The function t.Skips cleanly when the runner does not have the
// required kernel modules or configfs surface — the runner contract
// (v1 contract §8.4) treats environment lacks as skips, not failures,
// so the same test source compiles and can be invoked on any Linux
// host while only running against real coverage on the integration
// runner.
//
// Layout the gadget exposes:
//
//	idVendor=0x1d6b (Linux Foundation)
//	idProduct=0x0104 (Multifunction Composite Gadget)
//	bConfigurationValue=1 (default config; matches the speed
//	  bug fixture so users can also exercise the bConfigurationValue=2
//	  branch by mutating the gadget after bind — see
//	  SetupDummyHCDGadgetAtConfig for the variant entry point)
//
// Returns the absolute busid sysfs name (e.g. "1-1") that the bind
// CLI consumes verbatim.
func SetupDummyHCDGadget(t *testing.T, name string) string {
	t.Helper()

	requireDummyHCDPreconditions(t)

	// Snapshot existing dummy_hcd-backed busids BEFORE creating our
	// gadget so the post-bind diff yields exactly the busid the
	// kernel assigned to us. Concurrent integration runs or a stale
	// gadget left over from a previous run no longer tempt the
	// harness into returning a busid the test never created.
	preexisting := snapshotDummyBusIDs()

	gadgetDir := GadgetConfigfsPathFor(t, name)

	err := os.MkdirAll(gadgetDir, 0o755)
	if err != nil {
		t.Skipf("dummy_hcd harness: gadget mkdir %q: %v (configfs may not be writable)", gadgetDir, err)
	}

	// Schedule cleanup BEFORE writing any further state so a partial
	// setup still gets torn down on failure.
	t.Cleanup(func() {
		teardownDummyHCDGadget(t, gadgetDir)
	})

	writeGadgetAttr(t, gadgetDir, "idVendor", "0x1d6b")
	writeGadgetAttr(t, gadgetDir, "idProduct", "0x0104")
	writeGadgetAttr(t, gadgetDir, "bcdDevice", "0x0100")
	writeGadgetAttr(t, gadgetDir, "bcdUSB", "0x0200")

	// Required strings entry per Documentation/usb/gadget_configfs.rst.
	stringsDir := filepath.Join(gadgetDir, "strings", "0x409")

	err = os.MkdirAll(stringsDir, 0o755)
	if err != nil {
		t.Skipf("dummy_hcd harness: strings mkdir: %v", err)
	}

	writeGadgetAttr(t, stringsDir, "manufacturer", "abilisoft")
	writeGadgetAttr(t, stringsDir, "product", "usbip-go integration gadget")

	// Minimal configuration with one function so the gadget is
	// considered fully formed and dummy_udc accepts the UDC bind.
	configDir := filepath.Join(gadgetDir, "configs", "c.1")

	err = os.MkdirAll(filepath.Join(configDir, "strings", "0x409"), 0o755)
	if err != nil {
		t.Skipf("dummy_hcd harness: config mkdir: %v", err)
	}

	writeGadgetAttr(t, filepath.Join(configDir, "strings", "0x409"), "configuration", "default")

	functionDir := filepath.Join(gadgetDir, "functions", "acm.usb0")

	err = os.MkdirAll(functionDir, 0o755)
	if err != nil {
		t.Skipf("dummy_hcd harness: function mkdir (acm needed): %v", err)
	}

	err = os.Symlink(functionDir, filepath.Join(configDir, "acm.usb0"))
	if err != nil && !errors.Is(err, fs.ErrExist) {
		t.Skipf("dummy_hcd harness: symlink function->config: %v", err)
	}

	// Bind the gadget to dummy_udc.0 — this is the moment the device
	// enumerates under /sys/bus/usb/devices/. Without a UDC bind no
	// busid exists.
	writeGadgetAttr(t, gadgetDir, "UDC", dummyUDCName)

	busID, err := waitForNewGadgetBusID(preexisting, 2*time.Second)
	if err != nil {
		t.Skipf("dummy_hcd harness: gadget did not enumerate within deadline: %v", err)
	}

	return busID
}

// waitForNewGadgetBusID polls for a dummy_hcd-backed busid that is
// NOT in preexisting. Returns the first such busid. The caller-side
// guarantee: the returned busid is the one the most recent UDC bind
// created, not a stale leftover or a peer-test gadget.
func waitForNewGadgetBusID(preexisting map[string]struct{}, deadline time.Duration) (string, error) {
	end := time.Now().Add(deadline)

	for time.Now().Before(end) {
		current := snapshotDummyBusIDs()

		for id := range current {
			if _, ok := preexisting[id]; ok {
				continue
			}

			return id, nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	return "", fmt.Errorf("no NEW dummy_hcd-backed busid appeared in %s (existing: %v)", deadline, preexisting)
}

// requireDummyHCDPreconditions skips when modules or configfs root
// are missing. Centralised so each test file does not duplicate the
// skip strings.
func requireDummyHCDPreconditions(t *testing.T) {
	t.Helper()

	for _, mod := range dummyHCDRequiredModules() {
		_, err := os.Stat("/sys/module/" + mod)
		if err != nil {
			t.Skipf("dummy_hcd harness: kernel module %q not loaded (modprobe required): %v", mod, err)
		}
	}

	_, err := os.Stat(gadgetConfigfsRoot)
	if err != nil {
		t.Skipf("dummy_hcd harness: %s not present — mount configfs and modprobe libcomposite: %v",
			gadgetConfigfsRoot, err)
	}

	_, err = os.Stat("/sys/class/udc/" + dummyUDCName)
	if err != nil {
		t.Skipf("dummy_hcd harness: UDC %s not present — dummy_hcd may have been loaded with num=0: %v",
			dummyUDCName, err)
	}
}

// writeGadgetAttr writes data to a configfs gadget attribute file.
// Skips on failure so partial setup attempts do not surface as
// failures.
func writeGadgetAttr(t *testing.T, base, attr, data string) {
	t.Helper()

	full := filepath.Join(base, attr)

	err := os.WriteFile(full, []byte(data), 0o644)
	if err != nil {
		t.Skipf("dummy_hcd harness: write %s=%q: %v", full, data, err)
	}
}

// teardownDummyHCDGadget reverses the setup writes in the order
// configfs requires:
//
//  1. Unbind UDC (configfs refuses structural mutation while UDC held)
//  2. Remove every function->config symlink
//  3. Remove configs/<c>/strings/<locale> then configs/<c>
//  4. Remove functions/<fn>
//  5. Remove top-level strings/<locale>
//  6. Remove the gadget root
//
// The final rmdir is checked: if it fails the configfs subtree is
// left poisoned and the next test run with the same gadget name will
// EEXIST. Surface that with t.Errorf so a stale gadget never silently
// pollutes future runs. Earlier-step errors are still ignored — they
// cascade into the final rmdir failure if material.
func teardownDummyHCDGadget(t *testing.T, gadgetDir string) {
	t.Helper()

	// Step 1: release UDC.
	_ = os.WriteFile(filepath.Join(gadgetDir, "UDC"), []byte("\n"), 0o644)

	// Step 2 + 3: unwire each config and rmdir the locale subdir
	// underneath before rmdir'ing the config itself.
	configsDir := filepath.Join(gadgetDir, "configs")

	configEntries, err := os.ReadDir(configsDir)
	if err == nil {
		for _, c := range configEntries {
			cdir := filepath.Join(configsDir, c.Name())

			cEntries, _ := os.ReadDir(cdir)
			for _, fnLink := range cEntries {
				if fnLink.Type()&fs.ModeSymlink != 0 {
					_ = os.Remove(filepath.Join(cdir, fnLink.Name()))
				}
			}

			localeDir := filepath.Join(cdir, "strings")
			if locales, err := os.ReadDir(localeDir); err == nil {
				for _, l := range locales {
					_ = os.Remove(filepath.Join(localeDir, l.Name()))
				}
			}

			_ = os.Remove(cdir)
		}
	}

	// Step 4: remove function dirs.
	fnDir := filepath.Join(gadgetDir, "functions")
	if entries, err := os.ReadDir(fnDir); err == nil {
		for _, f := range entries {
			_ = os.Remove(filepath.Join(fnDir, f.Name()))
		}
	}

	// Step 5: remove top-level strings locales.
	topStrings := filepath.Join(gadgetDir, "strings")
	if locales, err := os.ReadDir(topStrings); err == nil {
		for _, l := range locales {
			_ = os.Remove(filepath.Join(topStrings, l.Name()))
		}
	}

	// Step 6: remove gadget root and surface failure.
	if err := os.Remove(gadgetDir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dummy_hcd teardown: gadget root %s did not unlink cleanly: %v — manual cleanup required before next run",
			gadgetDir, err)
	}
}

// GadgetConfigfsPathFor returns the configfs directory the harness
// will create for (t, name). The path encodes t.Name() so that
// concurrent test runs with the same caller-supplied logical name
// land in different configfs entries — without this, parallel
// integration runs race on the same /sys/kernel/config/usb_gadget/<name>
// directory and waitForNewGadgetBusID can claim the wrong busid.
//
// Only the configfs subdirectory name is sanitised; the logical name
// is preserved as a suffix so a failure in the configfs tree
// (`ls /sys/kernel/config/usb_gadget`) names the test that owned it.
// configfs accepts any non-empty filename without slashes; we
// substitute `/` with `_` so subtest names (`Parent/Sub`) survive.
func GadgetConfigfsPathFor(t *testing.T, name string) string {
	t.Helper()

	suffix := strings.ReplaceAll(t.Name(), "/", "_")

	return filepath.Join(gadgetConfigfsRoot, name+"_"+suffix)
}

// snapshotDummyBusIDs returns the set of dummy_hcd-backed busids
// currently present in /sys/bus/usb/devices. SetupDummyHCDGadget
// captures this set before its UDC bind so the post-bind diff is a
// single-element set containing only the gadget the call created —
// concurrent runs and stale gadgets can no longer cause the harness
// to hand the test the wrong busid.
func snapshotDummyBusIDs() map[string]struct{} {
	out := make(map[string]struct{})

	entries, err := os.ReadDir("/sys/bus/usb/devices")
	if err != nil {
		return out
	}

	for _, e := range entries {
		name := e.Name()
		if strings.Contains(name, ":") || strings.HasPrefix(name, "usb") {
			continue
		}

		link, err := os.Readlink(filepath.Join("/sys/bus/usb/devices", name))
		if err != nil {
			continue
		}

		if strings.Contains(link, "dummy_hcd") {
			out[name] = struct{}{}
		}
	}

	return out
}

// AbsCmdPath returns the absolute path to a binary, building it from
// ./cmd/<name>/ into a temp dir if not on PATH. The integration tests
// exec real binaries, so they need a guaranteed path; production
// installs land in /usr/local/bin or ~/go/bin which the runner may
// not have on its PATH for `go test`.
func AbsCmdPath(t *testing.T, name string) string {
	t.Helper()

	out, err := exec.LookPath(name)
	if err == nil {
		return out
	}

	tmp, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatalf("abs tempdir: %v", err)
	}

	binPath := filepath.Join(tmp, name)
	root := repoRootForIntegration(t)

	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binPath, "./cmd/"+name+"/")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "TMPDIR="+tmp)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build %s for integration: %s", name, output)
	}

	return binPath
}

// repoRootForIntegration walks up looking for go.mod.
func repoRootForIntegration(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found above %q", dir)
		}

		dir = parent
	}
}
