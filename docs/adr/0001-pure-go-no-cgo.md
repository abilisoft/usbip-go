# Pure Go, no cgo

USB/IP userspace tools traditionally use C (`usbip-utils` ships as a
Linux kernel sub-tree project). We chose a pure-Go implementation with
a hard ban on cgo for three reasons: static, cross-compiled binaries
with no libc dependency; reproducible builds on any Go toolchain
without a C compiler; and a test suite that runs on macOS and CI
runners that lack Linux kernel headers. The cost is that any future
need for direct kernel syscalls or ioctl surfaces not exposed through
sysfs would require either a subprocess shim or revisiting this
decision. All current kernel interaction goes through `/sys` file
reads/writes and netlink sockets, both of which are reachable from
pure Go.

The no-cgo rule is enforced by the `no-cgo` CI job (`go list -f
'{{.CgoFiles}}'` and a source grep for `import "C"`). A violation
fails the build rather than relying on code review.
