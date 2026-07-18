## MODIFIED Requirements

### Requirement: Release workflow only publishes canonical stable SemVer tags

The GitHub release workflow SHALL publish only tags matching
`vMAJOR.MINOR.PATCH`. The supported maintainer entry point SHALL be a direct
push of a signed annotated tag created once from the current default-branch
head. The validation job SHALL continue only when the push event identifies a
fresh tag creation with `created` equal to `true`, `forced` equal to `false`,
and `deleted` equal to `false`; the live ref is a GitHub-verified signed
annotated tag pointing directly to a commit; its name and target match the
event; and its target equals the checked-out default-branch head. The workflow
SHALL expose only a direct tag-push entry point, SHALL continue to publication
only after validating the fresh canonical stable tag, and SHALL NOT expose a
manual GitHub Actions launcher that cannot produce the required tag signature.
After initial validation, release and publication jobs SHALL check out the
immutable event commit without persisted Git credentials and SHALL revalidate
the live signed annotated tag against that commit immediately before staging or
publishing the draft release.
The active server-side tag ruleset SHALL remain the authoritative immutability
boundary because GitHub selects push workflow code from the event ref and
revision before the current workflow can validate it.

#### Scenario: Fresh stable tag is pushed

- **WHEN** a newly created signed tag such as `v1.2.3` is pushed directly
- **AND** the event reports `created=true`, `forced=false`, and `deleted=false`
- **AND** the current workflow revision verifies the annotated tag signature, name, commit target, event target, and default-branch head are identical where required
- **THEN** the release workflow is eligible to continue after the tag validation job

#### Scenario: Release source is consumed after validation

- **WHEN** the release or publication job checks out repository-owned release code after tag validation
- **THEN** it checks out the immutable event target rather than dereferencing the live tag
- **AND** the checkout does not persist GitHub credentials

#### Scenario: Live release tag changes after initial validation

- **WHEN** the live tag object, signature state, or commit target no longer matches the validated event immediately before draft staging or publication
- **THEN** the current release job fails before performing the protected operation
- **AND** it does not continue on the revalidation error

#### Scenario: Fresh stable tag targets the wrong commit

- **WHEN** a fresh canonical tag event reaches the current workflow but its commit target differs from the event target or checked-out default-branch head
- **THEN** the validate-tag job fails visibly before artifacts are built
- **AND** the version remains consumed and any later attempt uses a higher patch

#### Scenario: Release tag is lightweight or unverified

- **WHEN** a fresh canonical tag event reaches the current workflow but the live ref is not a GitHub-verified signed annotated tag pointing directly to a commit
- **THEN** the validate-tag job fails visibly before artifacts are built

#### Scenario: Existing stable tag move reaches the current workflow

- **WHEN** a stable tag push reports `created` other than `true` or `forced=true`
- **THEN** the validate-tag job fails visibly before artifacts are built
- **AND** no downstream release, provenance, or publication job runs

#### Scenario: Existing stable tag targets obsolete workflow code

- **WHEN** an actor attempts to move an existing stable tag to a revision whose release workflow predates current validation
- **THEN** the active server-side tag ruleset rejects the ref mutation
- **AND** maintainers do not use administrative bypass because the event-ref workflow cannot provide the current validation guarantee

#### Scenario: Stable tag deletion reaches validation

- **WHEN** a stable tag event reports `deleted` other than `false`
- **THEN** the validate-tag job fails visibly before artifacts are built
- **AND** no downstream release, provenance, or publication job runs

#### Scenario: Prerelease tag is pushed

- **WHEN** a tag such as `v1.2.3-rc1` is pushed
- **THEN** the workflow trigger excludes it from release publication

#### Scenario: Non-canonical tag reaches validation

- **WHEN** a tag such as `v1.2.3foo`, `v1.2.3+build.7`, or `v01.2.3` reaches validation
- **THEN** the validate-tag job rejects it before artifacts are built

### Requirement: Release notes come from git-cliff

The release workflow SHALL generate release notes for the exact stable release
tag at `HEAD` with git-cliff `--current` and redirect only the renderer's stdout
into the release-notes file. Bootstrap diagnostics and Make recipe echo SHALL
remain outside that file. The workflow SHALL fail before artifact publication
if the renderer's output is empty or its first heading does not identify the
pushed stable tag.

#### Scenario: Release notes render

- **WHEN** the release job checks out the immutable event target with full history and its stable tag is at `HEAD`
- **THEN** only `git-cliff --current --strip header` stdout writes `build/release-notes.md`
- **AND** setup and build diagnostics are excluded from the rendered release notes
- **AND** the first heading identifies the pushed stable tag
- **AND** that file is passed to GoReleaser through `--release-notes`

#### Scenario: Release notes are empty

- **WHEN** git-cliff renders zero bytes to stdout
- **THEN** `build/release-notes.md` remains zero bytes
- **AND** setup and build diagnostics do not make the file nonempty
- **AND** the workflow emits an error and refuses to release

#### Scenario: Release notes identify a different version

- **WHEN** the rendered release-notes heading does not identify the pushed stable tag
- **THEN** the workflow emits an error and refuses to stage release artifacts
