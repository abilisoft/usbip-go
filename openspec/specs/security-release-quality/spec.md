## Purpose

Specify the security posture, release integrity, CI gates, and repository quality controls that define usbip-go as it exists today.

## Requirements

### Requirement: USB/IP wire security is explicitly trusted-network only

The project SHALL treat USB/IP as plaintext, unauthenticated, and safe only on already-trusted networks or tunnels.

#### Scenario: Operator reads deployment guidance

- **WHEN** docs describe exposing port 3240
- **THEN** they warn against public internet or untrusted network exposure
- **AND** recommend private LAN, firewall, WireGuard, Tailscale, or SSH tunneling

### Requirement: TLS wrapping is out of scope

The project SHALL NOT add in-protocol TLS wrapping to USB/IP because it would break upstream interop and not cover kernel-owned URB traffic after handoff.

#### Scenario: Confidentiality is required

- **WHEN** an operator needs encrypted transport
- **THEN** docs direct them to tunnel the TCP connection outside usbip-go

### Requirement: Daemon defenses are defense-in-depth, not authentication

CIDR allow-listing, session caps, rate limits, handshake byte caps, and timeouts SHALL be documented and implemented as resource and exposure controls only.

#### Scenario: CIDR allow list is empty

- **WHEN** no `--allow-cidr` flag is configured
- **THEN** the daemon permits all peers to match upstream behavior

#### Scenario: CIDR allow list is non-empty

- **WHEN** a peer address is outside every allowed CIDR
- **THEN** the exporter rejects the handshake before kernel handoff

### Requirement: Privilege failures are actionable

Sysfs permission failures SHALL map to public permission sentinels and include context sufficient to diagnose missing privileges or capabilities.

#### Scenario: Caller lacks required privilege

- **WHEN** a sysfs write returns EPERM or EACCES
- **THEN** callers can classify the error as `ErrPermission`
- **AND** logs/errors include relevant capability/path context where available

### Requirement: Missing kernel modules are classified

Kernel module absence SHALL map to `ErrKernelModuleMissing` with role-appropriate module expectations.

#### Scenario: Importer module is missing

- **WHEN** importer code requires `vhci_hcd` or `usbip_core` and it is absent
- **THEN** the operation fails before unsafe work with the kernel-module sentinel

#### Scenario: Exporter module is missing

- **WHEN** exporter code requires `usbip_host` or `usbip_core` and it is absent
- **THEN** the operation fails before unsafe work with the kernel-module sentinel

### Requirement: Releases are reproducible and signed

Release workflows SHALL build pure-Go artifacts through GoReleaser, publish checksums, generate SBOM/provenance, and support keyless cosign verification.

#### Scenario: User verifies a release

- **WHEN** a user downloads a release archive
- **THEN** they can verify SLSA provenance, cosign bundle for checksums, and per-artifact sha256

### Requirement: CI enforces security scanning and pinned workflow posture

The repository SHALL run CodeQL, govulncheck, Trivy/Scorecard-style checks where configured, and keep GitHub Actions dependencies pinned unless a third-party reusable workflow explicitly requires a version tag for verifiable provenance. The default-branch ruleset SHALL require the current pull-request security, unit, conformance, coverage, architecture, TDD, CodeQL, Trivy, Scorecard, govulncheck, and codecov patch contexts.

#### Scenario: Vulnerability scan runs

- **WHEN** CI executes the nightly or pull-request workflows
- **THEN** `make govulncheck` participates in the Make/Bazel gate
- **AND** `make govulncheck-sarif` publishes SARIF for the govulncheck code-scanning context where GitHub credentials permit upload

#### Scenario: Workflow dependency is added

- **WHEN** a GitHub Actions `uses:` step is committed
- **THEN** it is pinned according to repository security posture conventions

#### Scenario: Required status check context changes

- **WHEN** a workflow job name changes
- **THEN** `docs/security-posture.md` and the default-branch ruleset required status-check context list are updated in the same change

#### Scenario: Pull request requires standalone security contexts

- **WHEN** a pull request targets `main`
- **THEN** CodeQL, Trivy, and Scorecard workflows run their required named analysis jobs
- **AND** SARIF/public-result publication is skipped when an untrusted fork lacks write credentials

### Requirement: CI enforces architecture and pure-Go constraints

The repository SHALL mechanically enforce DDD layering, no cgo, public API compatibility, cross-compilation, formatting, linting, spelling, Bazel/Starlark/TOML/OpenSpec validation, release-configuration validation, tests, and coverage thresholds through Bazel-backed Make targets.

#### Scenario: Adapter import crosses a forbidden boundary

- **WHEN** `internal/app` imports the kernel or transport adapter directly
- **THEN** the Bazel-backed architecture lint gate fails

#### Scenario: cgo is introduced

- **WHEN** any package contains cgo files or imports `C`
- **THEN** the Bazel-backed pure-Go enforcement gate fails

#### Scenario: Public API changes incompatibly

- **WHEN** `pkg/usbip` or `pkg/domain` changes incompatibly
- **THEN** API compatibility CI fails unless a Conventional Commit breaking marker (`!` in the subject or a `BREAKING CHANGE:` footer) and regenerated baseline accompany the change

### Requirement: Tests cover unit, conformance, integration, race, coverage, and mutation concerns

The repository SHALL organize tests across fast unit tests, wire conformance
tests, optional Linux integration tests, coverage gates, and mutation targets
for protocol-critical packages. The dedicated kernel workflow SHALL boot a
checksum-pinned full Linux kernel in a disposable guest VM as a narrow declared
non-hermetic prerequisite, then run the integration suite through the
Bazel-backed Make target inside that guest.

#### Scenario: Pull request changes executable lines

- **WHEN** Codecov evaluates repository-owned changed executable lines, excluding the generated third-party vendor tree
- **THEN** the `codecov/patch` gate requires 100% patch coverage with no threshold allowance
- **AND** repository-wide total and per-package coverage remain independently enforced

#### Scenario: Unit tests run

- **WHEN** `make test` runs
- **THEN** Bazel executes unit tests across command, public, internal, and test packages

#### Scenario: Integration tests run

- **WHEN** `make test-integration` runs on a capable Linux host
- **THEN** the integration test environment runs as root with writable configfs
- **AND** it provides the USB/IP, gadget-function, and `dummy_hcd` kernel modules required by every integration scenario
- **AND** the dedicated kernel workflow fails preflight instead of silently skipping a scenario when a required module is absent

#### Scenario: Hosted kernel prerequisites are provisioned

- **WHEN** the dedicated kernel workflow starts on its pinned GitHub-hosted Ubuntu image
- **THEN** it verifies and boots the checksum-pinned full-kernel guest image under QEMU
- **AND** it loads every required USB/IP, gadget-function, VUDC, VHCI, and dummy-host-controller module inside the guest
- **AND** it loads the host-side CDC ACM and USB mass-storage modules exercised by the imported gadgets
- **AND** it mounts configfs when needed
- **AND** it fails before tests if the gadget tree is absent or not writable

#### Scenario: Wire conformance tests run

- **WHEN** `make test-conformance` runs
- **THEN** USB/IP wire captures and synthetic upstream peers verify codec compatibility, skipping upstream-binary cross-checks only when the external `usbip` tool is unavailable

### Requirement: TDD and review discipline are codified

Contribution guidance SHALL require signed conventional commits without co-author trailers, TDD/RED-GREEN discipline as enforced by CI, no unexplained `nolint`, no unexplained skips, and reviewer checks for public API docs and tests.

#### Scenario: Pull request contains a policy-invalid commit

- **WHEN** a pull-request commit is unsigned, has a non-conventional subject, or contains a `Co-authored-by` trailer
- **THEN** the commit-discipline status check fails before merge

#### Scenario: Feature commit adds only tests

- **WHEN** a `feat:` or `fix:` commit adds tests but no production Go change outside allowed exceptions
- **THEN** the next commit must provide the green implementation or an accepted refactor according to the TDD gate

### Requirement: License and metadata are consistent

Source files SHALL carry Apache-2.0 SPDX headers and release metadata SHALL derive from conventional commits and git tags.

#### Scenario: Stable release tag is pushed

- **WHEN** a `vMAJOR.MINOR.PATCH` tag triggers release automation
- **THEN** release notes are generated from conventional commit history
