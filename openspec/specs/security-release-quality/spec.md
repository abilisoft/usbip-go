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

Release workflows SHALL build pure-Go artifacts through GoReleaser, publish
checksums, generate SBOM/provenance, and support keyless cosign verification.
Normal tag-push releases SHALL support verification against their stable source
tag. The fixed `v1.0.2` recovery SHALL support verification against the pinned
SLSA builder and protected-default-branch recovery workflow identity, while
independent artifact inspection SHALL confirm version `v1.0.2` and source commit
`72aa5a6b585d1f5b6230c8362254ea2a6296ec75`. Documentation SHALL distinguish
these event identities rather than claiming recovery provenance originated from
a new tag push.

#### Scenario: User verifies a normal release

- **WHEN** a user downloads an archive from a normal tag-push release
- **THEN** they can verify SLSA provenance against the source tag, the cosign bundle for checksums, and per-artifact sha256

#### Scenario: User verifies the recovered v1.0.2 release

- **WHEN** a user downloads an archive published by the fixed `v1.0.2` recovery
- **THEN** they can verify SLSA provenance against the pinned builder and protected recovery workflow identity
- **AND** they can verify the cosign bundle, per-artifact sha256, and exact `v1.0.2` source-commit stamping

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

The repository SHALL organize tests across fast unit tests, wire conformance tests, optional Linux integration tests, coverage gates, and mutation targets for protocol-critical packages. Focused Linux integration SHALL include bounded repeated single-kernel attachment lifecycle and deterministic 20 ms-per-chunk TCP-proxy cases. A separately tracked two-guest KVM test SHALL exercise the production exporter and importer paths across distinct guest kernels for exactly three bidirectional ACM attachment cycles with exactly 25 ms of delay on both dedicated guest egress paths. Three cycles SHALL constitute bounded regression coverage rather than soak or endurance evidence. Fixed delay SHALL NOT be treated as evidence for packet loss, jitter, reordering, bandwidth limiting, outages, or reconnect behavior under impairment. Kernel integration SHALL remain available through Bazel-backed Make targets for explicit manual execution on a capable Linux host, with KVM additionally required for the two-guest target, but SHALL NOT run in GitHub Actions because standard hosted runners lack the required KVM and privileged kernel surfaces.

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

#### Scenario: CLI integration crosses process boundaries

- **WHEN** the live CLI integration attaches a dummy_hcd-backed Device
- **THEN** it captures the JSON attach acknowledgement's PortID
- **AND** separate port and detach command processes inspect and release that exact Port
- **AND** it does not equate the exporter busid with VHCI local_busid

#### Scenario: GitHub Actions run on standard hosted runners

- **WHEN** pull-request, nightly, or release automation runs
- **THEN** it executes the unprivileged unit, conformance, architecture, security, coverage, mutation, and release checks applicable to that workflow
- **AND** it does not schedule kernel integration that requires root, writable configfs, or unavailable kernel modules

#### Scenario: Wire conformance tests run

- **WHEN** `make test-conformance` runs
- **THEN** USB/IP wire captures and synthetic upstream peers verify codec compatibility, skipping upstream-binary cross-checks only when the external `usbip` tool is unavailable

#### Scenario: Live-kernel lifecycle is cycled repeatedly

- **WHEN** `make test-integration` runs on a capable host
- **THEN** one Importer completes exactly three cycle-specific Attach, deterministic byte-transfer, exact-Port Detach, and kernel-drain cycles against fresh VUDC gadgets
- **AND** every cycle's bytes match exactly
- **AND** block disappearance requires `fs.ErrNotExist`, VHCI drain requires a nonempty valid all-`Null` snapshot, and gadget release errors fail the cycle
- **AND** the exact prior Port must be absent, `Null`, or `Available`; `NotAssigned` remains claimed

#### Scenario: Single-kernel live traffic crosses controlled proxy delay

- **WHEN** `make test-integration` routes a complete live-kernel USB/IP TCP stream through exactly 20 ms of bidirectional per-chunk delay
- **THEN** the import handshake and post-handoff URB traffic in both directions traverse that delayed stream
- **AND** deterministic payload transfer, exact-Port Detach, and kernel drain succeed

#### Scenario: Two-guest production resilience test runs

- **WHEN** `make test-integration-vm` runs on a capable Linux KVM host
- **THEN** its Bazel local/manual target boots distinct exporter and importer guest kernels with one vCPU and 1024 MiB per guest
- **AND** the exporter exercises production gadget bind and serve while the importer exercises production list, attach, port, and detach
- **AND** exactly three cycles transfer unique deterministic ACM payloads byte-for-byte in both directions
- **AND** each cycle detaches the exact returned Port and fails unless Port, imported device, and exporter session state drain before reuse
- **AND** exactly 25 ms of `tc netem` delay is active and records byte/packet-counter advancement on both dedicated guest egress paths
- **AND** any missing prerequisite, skip marker, ambiguous device, stale lifecycle state, or guest kernel fault fails the target
- **AND** success requires both roles still running and successful nonempty kernel, journal, system, and role-state evidence before failure scanning
- **AND** cleanup removes run state and overlays only after every guest is confirmed stopped and preserves them otherwise

#### Scenario: Two-guest host dependencies are narrowly non-hermetic

- **WHEN** the dedicated two-guest target prepares and runs its guests
- **THEN** repository inputs pin and cryptographically verify the guest image while caches remain under repository-local state
- **AND** access to KVM, QEMU, cloud-image and SSH tooling, image acquisition, and direct inter-guest networking is confined to the explicit local/manual target
- **AND** the target is excluded from default, pull-request, nightly, and release workflows on standard GitHub-hosted runners

### Requirement: Coverage evidence fails closed

The repository coverage gate SHALL reject an LCOV report whose aggregate contains no executable lines after configured exclusions are applied. An aggregate zero denominator MUST be treated as missing evidence, not as 100 percent coverage. A zero-line package record in an otherwise measurable report MUST be reported as not coverable and excluded from per-package threshold evaluation rather than assigned an invented percentage.

#### Scenario: Coverage report is empty

- **WHEN** the coverage checker receives an empty report or a report with no included `LF` records
- **THEN** it exits non-zero with an actionable missing-coverage error
- **AND** it does not print `0/0` as 100 percent coverage

#### Scenario: Zero-line and measured packages are mixed

- **WHEN** an LCOV report contains an included `LF:0` package and another included package whose measured lines satisfy the configured thresholds
- **THEN** the gate succeeds using the nonzero measured aggregate and package evidence
- **AND** it reports the zero-line package as not coverable without evaluating a package threshold or printing a `0/0` percentage

### Requirement: Human terminal output neutralizes untrusted controls

Human-oriented CLI rendering SHALL escape control code points from Device- or peer-controlled text before terminal styling is applied. Printable Unicode SHALL remain readable, and machine-readable JSON SHALL continue using JSON escaping without display-only mutation.

#### Scenario: Device descriptor contains terminal controls

- **WHEN** a Device manufacturer or product contains C0, DEL, C1, OSC, or CSI control code points
- **THEN** table output contains a visible escaped representation
- **AND** no untrusted executable terminal control reaches the output writer

#### Scenario: Device descriptor contains printable Unicode

- **WHEN** a Device manufacturer or product contains printable Unicode text
- **THEN** human output preserves that text

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
