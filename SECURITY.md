# Security Policy

## Supported Versions

| Version | Supported | Notes                                              |
| ------- | --------- | -------------------------------------------------- |
| 1.x     | Yes       | Latest minor of the v1 line; security fixes land here. |
| < 1.0   | No        | Pre-release; do not deploy.                        |

## Reporting a Vulnerability

**Please do not open a public issue for security reports.**

Use one of the private channels below:

1. **GitHub Security Advisories (preferred)**:
   <https://github.com/abilisoft/usbip-go/security/advisories/new>.
2. **Email**: `oss@abilisoft.com` with subject `usbip-go security`.
   PGP key on request.

Please include:

- A short description of the issue.
- A reproducer (commit SHA, command line, expected vs actual).
- A proof-of-concept where possible.

## Response Targets

- **Acknowledgement**: within 3 business days.
- **Triage decision** (accept / dispute / out-of-scope): within
  10 business days.
- **Fix released** for accepted high-severity reports: within
  30 days, coordinated with reporter on disclosure timing.

## Scope

In scope:

- The Go library (`pkg/usbip`, `pkg/domain`).
- The `usbip-go` and `usbipd-go` binaries shipped in releases.
- The systemd units in `contrib/systemd/`.
- The kernel-adapter code under `internal/adapter/kernel/`.

Out of scope:

- Vulnerabilities in upstream `usbip-utils` or the Linux kernel
  itself.
- USB/IP being a plaintext protocol — see
  [`docs/security.md`](docs/security.md). Deploy only on trusted
  networks.
- Issues that require root or `CAP_SYS_ADMIN` on the host running
  the daemon (those capabilities are already required for normal
  operation).

## Disclosure

We follow coordinated disclosure. We will not publicly disclose a
vulnerability before a fix is available unless the reporter
requests early disclosure or 90 days have elapsed without a fix.

CVEs are requested for accepted high-severity reports via the
GitHub Security Advisory pipeline.
