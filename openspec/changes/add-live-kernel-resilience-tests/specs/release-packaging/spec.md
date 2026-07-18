## MODIFIED Requirements

### Requirement: Release publication waits for prereq gates

The release job SHALL depend on reusable security, unit-test, conformance,
architecture, and coverage workflows that run on the standard GitHub-hosted
runner pool available to the project. Single-kernel module integration and
two-guest KVM resilience SHALL remain separate manual maintainer checks because
they require privileged Linux kernel or virtualization capabilities unavailable
on those runners.

#### Scenario: Prereq gate fails

- **WHEN** any prereq workflow fails for the tagged release
- **THEN** the draft-building release job does not run

#### Scenario: Prereq gates pass

- **WHEN** security, unit tests, conformance, architecture checks, and coverage complete successfully
- **THEN** the release job may build and stage draft artifacts for downstream attestation and publication

#### Scenario: Kernel integration requires privileged capabilities

- **WHEN** the project has only standard GitHub-hosted runners
- **THEN** the release workflow does not schedule kernel-module, writable-configfs, or KVM integration tests
- **AND** maintainers can run `make test-integration` on a capable Linux host and `make test-integration-vm` on a capable Linux KVM host
