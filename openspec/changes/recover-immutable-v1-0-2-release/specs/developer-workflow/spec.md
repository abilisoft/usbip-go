## MODIFIED Requirements

### Requirement: GitHub Actions use the Make/Bazel contract

GitHub Actions workflows SHALL invoke Make targets for repository build, test,
lint, vulnerability, mutation, and release operations so CI behavior matches
local contributor behavior. Kernel integration SHALL remain a manual Make target
for capable Linux hosts rather than a GitHub Actions job. The normal Release
workflow SHALL expose only a direct signed annotated tag-push entry point and
SHALL NOT offer a GitHub Actions launcher that cannot produce the tag signature
required by the repository ruleset. The separately named `v1.0.2` recovery
workflow SHALL be limited to its repository-fixed tag object and source commit,
SHALL accept only a fixed `v1.0.2` confirmation and no release-selection input,
and SHALL use the same Make/Bazel quality and release entry points against that
exact source.

#### Scenario: CodeQL traces the production binary

- **WHEN** the CodeQL workflow runs its manual Go build
- **THEN** it pins the `go.mod` toolchain and invokes `make CODEQL_GO=go build-codeql`
- **AND** the Make target builds only `./cmd/usbip-go` through CodeQL's injected Go wrapper because Bazel deliberately ignores the tracer's `LD_PRELOAD`
- **AND** the direct build isolates its caches under `.local/codeql`, disables ambient Go configuration, workspace discovery, and toolchain switching, and resolves dependencies from the synchronized vendor tree without network access

#### Scenario: Pull request CI runs

- **WHEN** the CI workflow runs for a push or pull request
- **THEN** reusable GitHub Actions jobs invoke Make/Bazel targets for security/lint/vulnerability, unit, conformance, coverage, architecture/API, and TDD discipline contexts required by the repository ruleset
- **AND** local contributors can exercise the repository-owned command sequence with `make ci-local`

#### Scenario: Nightly verification runs

- **WHEN** the nightly workflow runs
- **THEN** it invokes the reusable Make/Bazel security, unit, conformance, and coverage jobs
- **AND** it invokes `make release-snapshot`

#### Scenario: Tagged release runs

- **WHEN** the release workflow runs at a signed stable SemVer tag pushed from the current default-branch head
- **THEN** prereq jobs invoke the reusable Make/Bazel security, unit, conformance, coverage, and architecture/API gates for the peeled event commit
- **AND** the release staging job invokes `make ci-local`, `make changelog`, and `make release`
- **AND** release staging and publication use the workflow `GITHUB_TOKEN` and GoReleaser environment expected by the Bazel release target

#### Scenario: Normal manual release start is unavailable

- **WHEN** a maintainer inspects the normal Release workflow in the GitHub Actions UI
- **THEN** it exposes no `workflow_dispatch` entry point
- **AND** contributor instructions require a locally signed annotated tag push instead

#### Scenario: Fixed v1.0.2 recovery runs

- **WHEN** a maintainer dispatches the separately named `v1.0.2` recovery workflow from protected `main` with confirmation `v1.0.2`
- **THEN** its reusable prereq jobs receive only the repository-fixed immutable source commit
- **AND** its staging job invokes `make ci-local`, `make changelog`, and `make release` from a separate checkout of that commit
- **AND** the workflow does not accept an arbitrary version, tag, object, commit, or release identifier

### Requirement: Maintainers recover failed releases with a new version

Stable release versions and their signed annotated tags SHALL be single-use and
immutable after their first push. Maintainers SHALL NOT move, delete, recreate,
or bypass tag protection to reuse a stable version. If a pushed version is
incorrect, or a release fails after source or artifact correctness becomes
uncertain, a normal pull request SHALL retract that version in `go.mod`, and the
next release attempt SHALL use a higher patch version. The sole exception SHALL
be the repository-fixed recovery for `v1.0.2`, whose original run failed in the
first validation job before gates, artifact generation, draft creation,
provenance, or publication because annotated tag-object and peeled commit SHAs
were conflated. That recovery SHALL preserve the existing tag, exact source, all
quality gates, draft-first publication, and verifiable provenance. The active
server-side ruleset over all tags SHALL be the authoritative immutability
boundary; current workflow validation SHALL remain defense in depth because
push workflow code comes from the event ref and revision. Before consuming a
new stable version, maintainers SHALL verify that the Release workflow is
enabled and active.

#### Scenario: Pushed stable version is incorrect

- **WHEN** a stable version has been pushed and is found to contain incorrect or inconsistent source
- **THEN** maintainers leave its tag immutable and mark the version retracted through a pull request
- **AND** contributor and security guidance directs users to the next non-retracted stable version

#### Scenario: Automated release normally fails after tag creation

- **WHEN** any build, draft, provenance, or publication stage fails after a stable tag was pushed and the fixed `v1.0.2` exception does not apply
- **THEN** maintainers fix the failure through the protected default branch
- **AND** they retract the consumed version and create a new signed annotated tag with a higher patch version

#### Scenario: Original v1.0.2 validation fails before artifact work

- **WHEN** the immutable `v1.0.2` run fails only because its validator compares the annotated tag-object SHA with the peeled target commit
- **AND** no downstream gate, artifact, draft, provenance, or publication job ran
- **THEN** maintainers may merge and dispatch the fixed one-version recovery through protected `main`
- **AND** they preserve the original tag object and source commit exactly

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
