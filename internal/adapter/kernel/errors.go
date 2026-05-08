//go:build linux

package kernel

import "errors"

// Internal error sentinels. All user-visible errors surface domain
// sentinels (ErrDeviceNotFound, ErrKernelModuleMissing, ErrPermission,
// ErrDeviceAlreadyBound, ErrNoFreePort); these package-internal
// sentinels capture classes of failure that are only meaningful within
// the kernel adapter itself.
var (
	// errNonSysfsPath indicates a write attempt against a path outside
	// /sys/. Production writes must be rooted there; tests that want
	// arbitrary paths inject their own WriteFunc.
	errNonSysfsPath = errors.New("sysfs write rejected: path not rooted at /sys/")
)
