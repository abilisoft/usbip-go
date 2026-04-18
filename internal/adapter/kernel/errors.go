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

// errorsIsAny returns true iff err chains to any of the supplied
// targets via errors.Is. Keeps call sites concise where fs.ErrNotExist
// or a domain sentinel are both acceptable matches.
func errorsIsAny(err error, targets ...error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}

	return false
}
