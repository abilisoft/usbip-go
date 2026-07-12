## MODIFIED Requirements

### Requirement: Release workflow only publishes canonical stable SemVer tags

The GitHub release workflow SHALL publish only tags matching
`vMAJOR.MINOR.PATCH`. It SHALL accept either a direct matching tag push or a
manual GitHub Actions request from the current default-branch head. A manual
request SHALL create the tag and redispatch the release workflow at that tag so
both entry points use the same tag-context release jobs. The manual path SHALL
re-confirm the default-branch head immediately after tag creation and roll back
the new tag without dispatching if that confirmation fails or the branch moved.

#### Scenario: Stable tag is pushed

- **WHEN** a tag such as `v1.2.3` is pushed directly
- **THEN** the release workflow is eligible to continue after the tag validation job

#### Scenario: Stable release is started from GitHub Actions

- **WHEN** a maintainer manually runs the Release workflow from the current default-branch head with a tag such as `v1.2.3`
- **THEN** the workflow creates that tag at the validated commit
- **AND** it redispatches the same workflow with the new tag as its ref
- **AND** the tag-context run executes the same validation and release jobs as a direct tag push

#### Scenario: Manual release uses a non-default or already-stale ref

- **WHEN** a manual release request selects a non-default branch or a commit that is no longer the default-branch head
- **THEN** the workflow rejects the request before creating a tag

#### Scenario: Default branch advances during manual tag creation

- **WHEN** the default branch moves after the initial freshness check but before tag creation completes
- **THEN** the start script re-reads the default-branch head immediately after creating the tag
- **AND** it deletes the newly created tag, fails, and does not dispatch the tag-context workflow

#### Scenario: Manual release tag already exists

- **WHEN** the requested tag ref already exists
- **THEN** atomic ref creation fails and no second release workflow is dispatched

#### Scenario: Manual handoff fails

- **WHEN** the workflow creates a manual tag but cannot dispatch the tag-context release run
- **THEN** it deletes only the tag created by that request
- **AND** the start job fails

#### Scenario: Prerelease tag is pushed

- **WHEN** a tag such as `v1.2.3-rc1` is pushed
- **THEN** the workflow trigger excludes it from release publication

#### Scenario: Non-canonical tag reaches validation

- **WHEN** a tag such as `v1.2.3foo` or `v1.2.3+build.7` reaches either entry point
- **THEN** the workflow rejects it before artifacts are built

### Requirement: Release publication waits for prereq gates

The release job SHALL depend on reusable security, unit-test, conformance,
architecture, and coverage workflows that run on the standard GitHub-hosted
runner pool available to the project. Kernel integration SHALL remain a
separate manual maintainer check because it requires privileged Linux kernel
capabilities unavailable on those runners. These prerequisites SHALL be
identical for direct tag pushes and manually created tags.

#### Scenario: Prereq gate fails

- **WHEN** any prereq workflow fails for either release entry point
- **THEN** the build-and-publish release job does not run

#### Scenario: Prereq gates pass

- **WHEN** security, unit tests, conformance, architecture checks, and coverage complete successfully
- **THEN** the release job may build and publish artifacts

#### Scenario: Kernel integration requires privileged capabilities

- **WHEN** the project has only standard GitHub-hosted runners
- **THEN** the release workflow does not schedule kernel-module or writable-configfs integration tests
- **AND** maintainers can run `make test-integration` separately on a capable Linux host

### Requirement: Release notes come from git-cliff

The release workflow SHALL generate release notes for the stable release tag
with git-cliff and fail before artifact publication if the rendered notes are
empty.

#### Scenario: Release notes render

- **WHEN** the release job checks out the tag with full history
- **THEN** `git-cliff --latest --strip header` writes `build/release-notes.md`
- **AND** that file is passed to GoReleaser through `--release-notes`

#### Scenario: Release notes are empty

- **WHEN** `build/release-notes.md` is zero bytes
- **THEN** the workflow emits an error and refuses to release
