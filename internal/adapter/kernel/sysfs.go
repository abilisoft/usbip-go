// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/pkg/domain"
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

// Hex parsing radix and integer width used by ReadHex16.
const (
	hexRadix  = 16
	hex16Bits = 16
	dec10Bits = 32
	dec10     = 10
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

// fsPathFromAbs converts an absolute path (e.g. "/sys/bus/usb/devices")
// into an fs.FS-relative path. The fs.FS contract is rooted so the
// leading slash must be stripped — absent this, stdlib fs.Open returns
// an error for all /-prefixed paths regardless of the underlying FS.
func fsPathFromAbs(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}

	return p
}

// ReadLine opens path through fsys, reads the entire file, and returns
// the content with leading and trailing whitespace trimmed. Intended
// for one-line sysfs attributes; for multi-line readers the caller
// should tokenise higher up the stack.
func ReadLine(fsys fs.FS, path string) (string, error) {
	data, err := readFileBytes(fsys, path)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(data)), nil
}

// ReadHex16 reads a 16-bit hex-formatted sysfs attribute (e.g.
// idVendor, idProduct). Accepts both "0x0951" and "0951" forms;
// whitespace tolerated on either side.
func ReadHex16(fsys fs.FS, path string) (uint16, error) {
	line, err := ReadLine(fsys, path)
	if err != nil {
		return 0, err
	}

	s := strings.TrimPrefix(line, "0x")

	s = strings.TrimPrefix(s, "0X")

	v, perr := strconv.ParseUint(s, hexRadix, hex16Bits)
	if perr != nil {
		return 0, fmt.Errorf("parse hex16 %q: %w", path, perr)
	}

	return uint16(v), nil
}

// ReadUint reads a decimal uint32 from path.
func ReadUint(fsys fs.FS, path string) (uint32, error) {
	line, err := ReadLine(fsys, path)
	if err != nil {
		return 0, err
	}

	v, perr := strconv.ParseUint(line, dec10, dec10Bits)
	if perr != nil {
		return 0, fmt.Errorf("parse uint %q: %w", path, perr)
	}

	return uint32(v), nil
}

// ListDirEntries returns the names of every entry in the directory at
// path, sorted lexicographically (fs.ReadDir already guarantees sorted
// order; we return the slice of names directly). Errors during open
// go through the errno classifier.
func ListDirEntries(fsys fs.FS, path string) ([]string, error) {
	entries, err := fs.ReadDir(fsys, fsPathFromAbs(path))
	if err != nil {
		return nil, classifyFSErr("readdir", path, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	sort.Strings(names)

	return names, nil
}

// readFileBytes opens path via fsys and returns its full contents. The
// fs.FS contract strips the leading slash; errors are routed through
// the kernel-specific classifier so caller sites see domain sentinels
// without extra wrapping.
func readFileBytes(fsys fs.FS, path string) ([]byte, error) {
	f, err := fsys.Open(fsPathFromAbs(path))
	if err != nil {
		return nil, classifyFSErr("open sysfs", path, err)
	}

	defer func() { _ = f.Close() }()

	data, rerr := io.ReadAll(f)
	if rerr != nil {
		return nil, classifyFSErr("read sysfs", path, rerr)
	}

	return data, nil
}

// pathKind classifies a sysfs path for errno-dispatch. Per spec
// §11.5.4 the adapter must surface ErrKernelModuleMissing when a
// driver, module, or controller path disappears mid-flight; absent a
// classifier we would report ErrDeviceNotFound uniformly.
type pathKind uint8

const (
	// kindDevice covers /sys/bus/usb/devices/<busid>/... — ENOENT here
	// means the USB device is gone (unplugged or not enumerated).
	kindDevice pathKind = iota
	// kindDriver covers /sys/bus/usb/drivers/usbip-host/... — ENOENT
	// here means usbip-host is not loaded.
	kindDriver
	// kindController covers /sys/devices/platform/vhci_hcd.0/... —
	// ENOENT here means vhci_hcd is not loaded.
	kindController
	// kindModule covers /sys/module/<name> — ENOENT here means the
	// module is not loaded.
	kindModule
	// kindOther is the fallback; ENOENT on these paths is not
	// classified as a domain sentinel and the raw syscall error
	// surfaces.
	kindOther
)

// classifyPath inspects path and returns its pathKind. The classifier
// is pure and independent of fsys state; it looks at prefix shape
// alone.
func classifyPath(path string) pathKind {
	switch {
	case strings.HasPrefix(path, SysfsUSBDevices+"/"), path == SysfsUSBDevices:
		return kindDevice
	case strings.HasPrefix(path, SysfsUSBIPHostDriver+"/"), path == SysfsUSBIPHostDriver:
		return kindDriver
	case strings.HasPrefix(path, SysfsVHCIHCD+"/"), path == SysfsVHCIHCD:
		return kindController
	case strings.HasPrefix(path, SysfsModuleDir+"/"), path == SysfsModuleDir:
		return kindModule
	default:
		return kindOther
	}
}

// ClassifyErrno maps a raw errno against path-kind into a domain
// sentinel. Exported for tests; production call sites go through
// classifyFSErr / classifySyscallErr which wrap the result with
// context.
func ClassifyErrno(path string, errno unix.Errno) error {
	return classifyErrnoKind(classifyPath(path), errno)
}

// classifyErrnoKind is the kind-aware core of the classifier.
func classifyErrnoKind(kind pathKind, errno unix.Errno) error {
	switch errno {
	case unix.EACCES, unix.EPERM:
		return fmt.Errorf("%w: %w", domain.ErrPermission, errno)
	case unix.EBUSY:
		return fmt.Errorf("%w: %w", domain.ErrDeviceAlreadyBound, errno)
	case unix.ENODEV:
		return fmt.Errorf("%w: %w", domain.ErrDeviceNotFound, errno)
	case unix.ENOENT:
		return classifyENOENT(kind, errno)
	default:
		return fmt.Errorf("sysfs errno: %w", errno)
	}
}

// classifyENOENT dispatches ENOENT on pathKind. Device-paths surface
// ErrDeviceNotFound; driver/controller/module-paths surface
// ErrKernelModuleMissing. Other kinds return ENOENT unchanged.
func classifyENOENT(kind pathKind, errno unix.Errno) error {
	switch kind {
	case kindDevice:
		return fmt.Errorf("%w (ENOENT)", domain.ErrDeviceNotFound)
	case kindDriver, kindController, kindModule:
		return fmt.Errorf("%w (ENOENT)", domain.ErrKernelModuleMissing)
	case kindOther:
		return fmt.Errorf("sysfs ENOENT: %w", errno)
	default:
		return fmt.Errorf("sysfs ENOENT: %w", errno)
	}
}

// classifyFSErr wraps an fs-layer error with context and routes it
// through the errno classifier when it can be extracted. fs.ErrNotExist
// wrapping is also mapped (testing/fstest.MapFS surfaces fs.ErrNotExist
// rather than unix.ENOENT).
func classifyFSErr(op, path string, err error) error {
	classified := classifyErrAny(path, err)

	return fmt.Errorf("%s %q: %w", op, path, classified)
}

// classifySyscallErr is the write-side companion of classifyFSErr.
func classifySyscallErr(op, path string, err error) error {
	return classifyFSErr(op, path, err)
}

// classifyErrAny reduces an arbitrary error to its domain-sentinel
// equivalent if the underlying cause is an errno or fs.ErrNotExist.
// Unrecognised errors pass through unchanged.
func classifyErrAny(path string, err error) error {
	var errno unix.Errno
	if errors.As(err, &errno) {
		return classifyErrnoKind(classifyPath(path), errno)
	}

	if errors.Is(err, fs.ErrNotExist) {
		return classifyErrnoKind(classifyPath(path), unix.ENOENT)
	}

	if errors.Is(err, fs.ErrPermission) {
		return classifyErrnoKind(classifyPath(path), unix.EACCES)
	}

	return err
}
