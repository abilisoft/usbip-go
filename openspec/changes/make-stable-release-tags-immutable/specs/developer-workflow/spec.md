## ADDED Requirements

### Requirement: Maintainers recover failed releases with a new version

Stable release versions and their signed annotated tags SHALL be single-use and
immutable after their first push. Maintainers SHALL NOT move, delete, recreate,
or bypass tag protection to reuse a stable version. If a pushed version is
incorrect or its release fails, a normal pull request SHALL retract that
version in `go.mod`, and the next release attempt SHALL use a higher patch
version. The active server-side ruleset over all tags SHALL be the authoritative
immutability boundary; current workflow validation SHALL remain defense in
depth because push workflow code comes from the event ref and revision. Before
consuming a new stable version, maintainers SHALL verify that the Release
workflow is enabled and active.

#### Scenario: Pushed stable version is incorrect

- **WHEN** a stable version has been pushed and is found to contain incorrect or inconsistent source
- **THEN** maintainers leave its tag immutable and mark the version retracted through a pull request
- **AND** contributor and security guidance directs users to the next non-retracted stable version

#### Scenario: Automated release fails after tag creation

- **WHEN** any build, draft, provenance, or publication stage fails after a stable tag was pushed
- **THEN** maintainers fix the failure through the protected default branch
- **AND** they retract the consumed version and create a new signed annotated tag with a higher patch version

#### Scenario: Tag-protection bypass is available

- **WHEN** a maintainer has administrative ability to bypass the tag ruleset
- **THEN** routine release and recovery operations do not use that bypass to move, delete, or recreate a stable tag

#### Scenario: Prohibited tag event selects obsolete workflow code

- **WHEN** moving or recreating a stable tag could select release workflow code that predates current validation
- **THEN** maintainers rely on the active all-tag ruleset to reject the mutation
- **AND** they do not represent current workflow validation as an authoritative immutability boundary

#### Scenario: Release automation is disabled

- **WHEN** the Release workflow is not enabled and active during preflight
- **THEN** maintainers restore and verify it before pushing a new stable tag
- **AND** they do not consume a version while its automation is disabled
