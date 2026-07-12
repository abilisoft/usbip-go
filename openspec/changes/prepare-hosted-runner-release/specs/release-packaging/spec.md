## MODIFIED Requirements

### Requirement: Release publication waits for prereq gates

The release job SHALL depend on reusable security, unit-test, conformance,
architecture, coverage, and dedicated kernel-integration workflows. The
kernel-integration prerequisite SHALL be schedulable on the standard
GitHub-hosted runner pool available to the project.

#### Scenario: Prereq gate fails

- **WHEN** any prereq workflow fails
- **THEN** the build-and-publish release job does not run

#### Scenario: Prereq gates pass

- **WHEN** security, unit tests, conformance, architecture checks, coverage, and dedicated kernel integration complete successfully
- **THEN** the release job may build and publish artifacts

#### Scenario: Only standard hosted runners are available

- **WHEN** the repository has no self-hosted runner
- **THEN** the dedicated kernel-integration prerequisite runs on the pinned standard GitHub-hosted Ubuntu image
- **AND** release publication remains gated on its success
