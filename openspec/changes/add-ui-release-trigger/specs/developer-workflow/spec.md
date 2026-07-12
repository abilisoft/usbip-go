## MODIFIED Requirements

### Requirement: GitHub Actions use the Make/Bazel contract

GitHub Actions workflows SHALL invoke Make targets for repository build, test,
lint, vulnerability, mutation, and release operations so CI behavior matches
local contributor behavior. Kernel integration SHALL remain a manual Make target
for capable Linux hosts rather than a GitHub Actions job. The release workflow
SHALL expose both direct tag-push and GitHub Actions manual entry points, and
both SHALL converge on the same tag-context Make/Bazel release jobs.

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

- **WHEN** the release workflow runs at a stable SemVer tag created by either supported entry point
- **THEN** prereq jobs invoke the reusable Make/Bazel security, unit, conformance, coverage, and architecture/API gates
- **AND** the publish job invokes `make ci-local`, `make changelog`, and `make release`
- **AND** release publication uses the workflow `GITHUB_TOKEN` and GoReleaser environment expected by the Bazel release target

#### Scenario: Manual release start runs

- **WHEN** a maintainer starts a stable release from the GitHub Actions UI
- **THEN** the repository-owned start script validates and creates the tag, confirms the default-branch head did not move during creation, and only then redispatches the tag-context workflow
- **AND** hermetic script tests cover success, validation failures, API failures, concurrent default-branch movement, and handoff rollback
