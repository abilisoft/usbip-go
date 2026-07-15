## MODIFIED Requirements

### Requirement: Release publication waits for prereq gates

The release job SHALL depend on reusable security, unit-test, conformance,
architecture, and coverage workflows that run on the standard GitHub-hosted
runner pool available to the project. Single-kernel module integration and
two-guest KVM resilience SHALL remain separate manual maintainer checks because
they require privileged Linux kernel or virtualization capabilities unavailable
on those runners. These prerequisites SHALL be identical for direct tag pushes
and manually created tags.

#### Scenario: Prereq gate fails

- **WHEN** any prereq workflow fails for either release entry point
- **THEN** the build-and-publish release job does not run

#### Scenario: Prereq gates pass

- **WHEN** security, unit tests, conformance, architecture checks, and coverage complete successfully
- **THEN** the release job may build and publish artifacts

#### Scenario: Kernel integration requires privileged capabilities

- **WHEN** the project has only standard GitHub-hosted runners
- **THEN** the release workflow does not schedule kernel-module, writable-configfs, or KVM integration tests
- **AND** maintainers can run `make test-integration` on a capable Linux host and `make test-integration-vm` on a capable Linux KVM host
