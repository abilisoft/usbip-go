## MODIFIED Requirements

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
- **AND** it mounts configfs when needed
- **AND** it fails before tests if the gadget tree is absent or not writable

#### Scenario: Wire conformance tests run

- **WHEN** `make test-conformance` runs
- **THEN** USB/IP wire captures and synthetic upstream peers verify codec compatibility, skipping upstream-binary cross-checks only when the external `usbip` tool is unavailable
