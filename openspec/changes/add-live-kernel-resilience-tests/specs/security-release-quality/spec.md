## MODIFIED Requirements

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
