# pkg/usbip is the sole public entry point

External consumers import only `pkg/usbip`. `pkg/domain` types are
returned across the boundary (aliased, not redeclared), but no
`internal/` package is ever reachable from outside the module.

The alternative — letting consumers import `internal/app` directly
and compose their own adapter wiring — was rejected because it would
expose every adapter interface as a semver-stable surface. Adapter
interfaces change more often than use-case signatures (kernel sysfs
layouts shift, protocol codec methods gain parameters). Keeping the
single facade as the only import target means internal churn does not
force a major version bump on library consumers.

The rule is enforced by Go's own `internal/` package visibility
restriction, which is compiler-level and cannot be bypassed.
