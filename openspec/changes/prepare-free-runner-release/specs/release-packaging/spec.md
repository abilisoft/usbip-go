## MODIFIED Requirements

### Requirement: Release publication waits for prereq gates

The release job SHALL depend on reusable security, unit-test, conformance,
architecture, and coverage workflows that run on the standard GitHub-hosted
runner pool available to the project. Kernel integration SHALL remain a
separate manual maintainer check because it requires privileged Linux kernel
capabilities unavailable on those runners.

#### Scenario: Prereq gate fails

- **WHEN** any prereq workflow fails
- **THEN** the build-and-publish release job does not run

#### Scenario: Prereq gates pass

- **WHEN** security, unit tests, conformance, architecture checks, and coverage complete successfully
- **THEN** the release job may build and publish artifacts

#### Scenario: Kernel integration requires privileged capabilities

- **WHEN** the project has only standard GitHub-hosted runners
- **THEN** the release workflow does not schedule kernel-module or writable-configfs integration tests
- **AND** maintainers can run `make test-integration` separately on a capable Linux host
