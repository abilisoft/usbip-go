## MODIFIED Requirements

### Requirement: Release workflow only publishes canonical stable SemVer tags

The GitHub release workflow SHALL publish only tags matching
`vMAJOR.MINOR.PATCH`. The supported maintainer entry point SHALL be a direct
push of a signed annotated tag created once from the current default-branch
head. The validation job SHALL continue only when the push event identifies a
fresh tag creation with `created` equal to `true`, `forced` equal to `false`,
and `deleted` equal to `false`; the live ref is a GitHub-verified signed
annotated tag pointing directly to a commit; its name matches the event tag;
its tag-object SHA matches `github.event.after`; and its peeled target commit
matches both `github.sha` and the checked-out default-branch head. The workflow
SHALL expose only a direct tag-push entry point, SHALL continue to publication
only after validating the fresh canonical stable tag, and SHALL NOT expose a
manual GitHub Actions launcher that cannot produce the required tag signature.
After initial validation, release and publication jobs SHALL check out the
immutable peeled event commit from `github.sha` without persisted Git
credentials and SHALL revalidate the live signed annotated tag object and its
target immediately before staging or publishing the draft release. The active
server-side tag ruleset SHALL remain the authoritative immutability boundary
because GitHub selects push workflow code from the event ref and revision
before the current workflow can validate it.

#### Scenario: Fresh stable tag is pushed

- **WHEN** a newly created signed tag such as `v1.2.3` is pushed directly
- **AND** the event reports `created=true`, `forced=false`, and `deleted=false`
- **AND** the current workflow revision verifies the annotated tag signature and name
- **AND** the live tag-object SHA equals `github.event.after`
- **AND** the live tag target equals `github.sha` and the checked-out default-branch head
- **THEN** the release workflow is eligible to continue after the tag validation job

#### Scenario: Release source is consumed after validation

- **WHEN** the release or publication job checks out repository-owned release code after tag validation
- **THEN** it checks out the peeled immutable event commit from `github.sha` rather than the tag-object SHA or live tag ref
- **AND** the checkout does not persist GitHub credentials

#### Scenario: Live release tag changes after initial validation

- **WHEN** the live tag object, signature state, or commit target no longer matches the validated event immediately before draft staging or publication
- **THEN** the current release job fails before performing the protected operation
- **AND** it does not continue on the revalidation error

#### Scenario: Annotated tag object and target have distinct identities

- **WHEN** an annotated stable-tag push reports one SHA for `github.event.after` and a different peeled commit for `github.sha`
- **THEN** validation compares the live ref-object SHA only with `github.event.after`
- **AND** compares the annotated object's direct commit target only with `github.sha` and the checked-out commit

#### Scenario: Fresh stable tag targets the wrong commit

- **WHEN** a fresh canonical tag event reaches the current workflow but its commit target differs from `github.sha` or the checked-out default-branch head
- **THEN** the validate-tag job fails visibly before artifacts are built
- **AND** the version remains consumed and follows the failed-release recovery policy

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

### Requirement: SLSA provenance covers user-downloadable binary artifacts

The release and fixed recovery workflows SHALL produce SLSA provenance for
downloadable binary archives and OS packages. They SHALL invoke the generic
generator with the exact verifier-compatible reusable-workflow identity
`slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@v2.1.0`.
GoReleaser SHALL stage and reuse one draft GitHub Release. The provenance job
SHALL upload into that existing release with `draft-release: 'true'`; the fixed
recovery workflow SHALL additionally use `upload-tag-name` for its immutable
tag. Only a publish job that depends on successful provenance SHALL make the
release public. Verification guidance SHALL distinguish normal tag-push
provenance from protected-default-branch recovery provenance and SHALL NOT
represent one event identity as the other.

#### Scenario: Artifact hashes are collected

- **WHEN** GoReleaser has produced release output under `build/dist`
- **THEN** the release job hashes `*.tar.gz`, `*.deb`, and `*.rpm` artifacts
- **AND** exposes the base64-encoded sha256 list to the provenance job

#### Scenario: Provenance is generated

- **WHEN** the provenance job runs
- **THEN** the SLSA generic generator at `@v2.1.0` uses the release job's artifact hashes
- **AND** uploads provenance assets to the existing draft GitHub Release
- **AND** keeps that release unpublished until the dependent publish job runs

#### Scenario: Recovery provenance is generated

- **WHEN** the fixed `v1.0.2` recovery provenance job runs from protected `main`
- **THEN** the generator uploads the exact artifact subjects to the `v1.0.2` draft through `upload-tag-name`
- **AND** its signed invocation identifies the recovery workflow and protected default-branch revision
- **AND** verification combines that identity with the fixed validated source commit and artifact version/commit stamping

#### Scenario: Provenance generation fails

- **WHEN** the SLSA generator fails before uploading valid provenance
- **THEN** the draft GitHub Release remains unpublished
- **AND** the dependent publish job does not run

## ADDED Requirements

### Requirement: The immutable v1.0.2 release has a fixed fail-closed recovery

The repository SHALL provide one fixed-input `workflow_dispatch` recovery for
the already-consumed `v1.0.2` tag. Its sole confirmation input SHALL accept only
`v1.0.2`; it SHALL accept no user-selected object, source commit, or release
identifier. It SHALL run only from protected `main` and bind exactly the tag name
`v1.0.2`, annotated tag-object SHA
`f0c7083fdee40e1e31ebc170992fa5f43efe8d60`, and direct target commit
`72aa5a6b585d1f5b6230c8362254ea2a6296ec75`. It SHALL query and revalidate the
live GitHub ref, verified signature, annotated object, and direct commit target;
test and build the exact target commit separately from current controller code;
stage only an exact-tag draft and bind its immutable release ID; generate
provenance for exactly nine nonempty archive/package subjects; and revalidate
the same release ID immediately before publication. Final validation SHALL
require the exact 15-asset roster, require valid GitHub SHA-256 digests for all
assets, require all 14 GoReleaser assets to match their locally staged hashes,
and independently require each remote archive/package digest to equal the
subject hash given to the SLSA generator. It SHALL never move, delete, or
recreate the tag and SHALL become inert once an exact-tag public release exists.

#### Scenario: Exact immutable recovery preflight passes

- **WHEN** the recovery is dispatched from protected `main` with confirmation `v1.0.2`
- **AND** the live `v1.0.2` ref is the exact verified annotated object and direct commit target
- **AND** no public `v1.0.2` release exists
- **THEN** it exports the fixed source commit to the normal hosted prerequisite gates
- **AND** each gate checks out that exact commit without persisted credentials

#### Scenario: Recovery source is staged

- **WHEN** every hosted prerequisite gate succeeds for the validated source commit
- **THEN** protected controller code checks out the exact source separately
- **AND** source-scoped commands invoke `make ci-local`, `make changelog`, and `make release`
- **AND** the draft remains unpublished until provenance succeeds

#### Scenario: Recovery identity changes

- **WHEN** the live tag-object SHA, tag signature, direct target, controller ref, checked-out source commit, or exact release state differs from the fixed recovery contract
- **THEN** recovery fails before the next protected operation
- **AND** it does not publish or mutate the stable tag

#### Scenario: Recovery is replayed after publication

- **WHEN** an exact-tag public `v1.0.2` release already exists
- **THEN** recovery preflight fails visibly
- **AND** no gate, build, draft replacement, provenance, or publication job continues

#### Scenario: Recovery publication is attempted

- **WHEN** the fixed recovery has staged its exact-tag draft and provenance succeeds
- **THEN** it reuses the bound draft release ID captured immediately after staging
- **AND** it requires the exact 15 nonempty uploaded release assets and no unexpected asset
- **AND** every asset has a valid GitHub SHA-256 digest
- **AND** all 14 remote GoReleaser asset digests equal their locally staged hashes
- **AND** all nine remote archive/package digests equal the exact subject hashes given to the provenance generator
- **AND** final validation rechecks the live immutable tag and source target
- **AND** publication changes only that same validated draft ID's `draft` state to public
