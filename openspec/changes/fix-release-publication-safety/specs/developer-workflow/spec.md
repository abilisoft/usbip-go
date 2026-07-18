## MODIFIED Requirements

### Requirement: GitHub Actions use the Make/Bazel contract

GitHub Actions workflows SHALL invoke Make targets for repository build, test,
lint, vulnerability, mutation, and release operations so CI behavior matches
local contributor behavior. Kernel integration SHALL remain a manual Make target
for capable Linux hosts rather than a GitHub Actions job. The release workflow
SHALL expose only a direct signed annotated tag-push entry point. It SHALL NOT
offer a GitHub Actions launcher that cannot produce the tag signature required
by the repository ruleset.

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
- **THEN** prereq jobs invoke the reusable Make/Bazel security, unit, conformance, coverage, and architecture/API gates
- **AND** the release staging job invokes `make ci-local`, `make changelog`, and `make release`
- **AND** release staging and publication use the workflow `GITHUB_TOKEN` and GoReleaser environment expected by the Bazel release target

#### Scenario: Manual release start is unavailable

- **WHEN** a maintainer inspects the Release workflow in the GitHub Actions UI
- **THEN** it exposes no `workflow_dispatch` entry point
- **AND** contributor instructions require a locally signed annotated tag push instead
