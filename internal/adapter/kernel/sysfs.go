//go:build linux

package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sysfsFileMode is the permission mask we ask os.OpenFile to apply on
// open; sysfs files exist with pre-set permissions so the mode argument
// is ignored by the kernel, but O_WRONLY mandates a mode for portability.
const sysfsFileMode os.FileMode = 0o600

// sysfsRoot is the fixed prefix every production write path must share.
// The guard in writeSysfsFile enforces it so G304's concern (arbitrary
// path inclusion) cannot materialise — only paths under /sys are
// acceptable to the production default.
const sysfsRoot = "/sys/"

// writeSysfsFile opens path with O_WRONLY and writes data verbatim.
// This is the production default injected as WriteFunc by
// defaultWriteFunc(); Task 4.2's sysfs read primitives layer the errno
// classification on top of this primitive for both read and write
// paths.
//
// Rejects any path not rooted at /sys/ and any path containing ".."
// segments so gosec G304's concern (variable path into os.OpenFile)
// is addressed by construction rather than suppression.
func writeSysfsFile(path, data string) error {
	cleaned := filepath.Clean(path)

	if !strings.HasPrefix(cleaned, sysfsRoot) {
		return fmt.Errorf("write sysfs %q: %w", path, errNonSysfsPath)
	}

	f, err := os.OpenFile(cleaned, os.O_WRONLY, sysfsFileMode)
	if err != nil {
		return fmt.Errorf("open sysfs %q: %w", path, err)
	}

	_, werr := f.WriteString(data)
	cerr := f.Close()

	if werr != nil {
		return fmt.Errorf("write sysfs %q: %w", path, werr)
	}

	if cerr != nil {
		return fmt.Errorf("close sysfs %q: %w", path, cerr)
	}

	return nil
}
