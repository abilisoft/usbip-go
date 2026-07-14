## MODIFIED Requirements

### Requirement: Tests cover unit, conformance, integration, race, coverage, and mutation concerns

The repository SHALL organize tests across fast unit tests, wire conformance tests, optional Linux integration tests, coverage gates, and mutation targets for protocol-critical packages. Kernel integration SHALL remain available through the Bazel-backed Make target for manual execution on a capable Linux host, but SHALL NOT run in GitHub Actions because standard hosted runners lack the required privileged kernel surface.

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
