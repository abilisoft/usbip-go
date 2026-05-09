# DDD layered architecture with CI-enforced boundaries

The codebase is organized into five strictly ordered layers: CLI
entrypoints (`cmd/`) → public facade (`pkg/usbip`) → application
services (`internal/app`) → adapters (`internal/adapter/{kernel,wire,
transport}`) → pure domain (`pkg/domain`). Dependencies only flow
top-down; no adapter package imports `internal/app`, and `pkg/domain`
imports nothing from `internal/`.

We chose this shape because the sysfs and netlink interfaces are Linux-
only and volatile — they have broken across kernel versions before.
Keeping the application logic isolated behind declared interfaces means
the kernel adapter can be replaced (e.g. for a future ioctl surface or
a mock) without touching the use-case code. The facade layer exists
separately so the internal packages can restructure freely without
breaking the public API semver contract.

The boundaries are mechanically enforced by the `domain-boundary` CI
job (in the `_arch-checks.yml` reusable workflow, called from
`ci.yml`'s `arch:` job) rather than by code review alone. This was a deliberate choice after
earlier drafts found that even careful reviewers let indirect imports
slip through in test files.

**Considered options:**
- Flat package layout (everything in `pkg/usbip`) — rejected: the
  kernel adapter would become untestable without a Linux host.
- Two layers (public + internal) — rejected: `internal/app` would have
  to import `internal/adapter/kernel` directly, coupling use-case logic
  to a Linux-only package.
