// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// writeSysfsFile opens path with O_WRONLY and writes data verbatim.
// This is the production default injected as WriteFunc by
// defaultWriteFunc(); the sysfs read primitives layer the errno
// classification on top of this primitive for both read and write
// paths.
//
// Rejects any path not rooted at /sys/ and any path containing ".."
// segments so gosec G304's concern (variable path into os.OpenFile)
// is addressed by construction rather than suppression.
//
// Lives in its own file so the project's coverage gate can carve it
// out without scattering coverage-ignore annotations: the function
// dispatches real syscalls against /sys whose error branches
// (EFAULT, ENOMEM, EBADF mid-write, EBUSY-style classifications)
// cannot be synthesised hermetically. The integration suite
// exercises them in the manual kernel-integration environment. See
// `.testcoverage.yaml` exclude list for the matched path.
func writeSysfsFile(path, data string) error {
	cleaned := filepath.Clean(path)

	if !strings.HasPrefix(cleaned, sysfsRoot) {
		return fmt.Errorf("write sysfs %q: %w", path, errNonSysfsPath)
	}

	f, err := os.OpenFile(cleaned, os.O_WRONLY, sysfsFileMode)
	if err != nil {
		return classifySyscallErr("open sysfs", path, err)
	}

	_, werr := f.WriteString(data)
	cerr := f.Close()

	if werr != nil {
		return classifySyscallErr("write sysfs", path, werr)
	}

	if cerr != nil {
		return classifySyscallErr("close sysfs", path, cerr)
	}

	return nil
}
