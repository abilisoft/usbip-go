## Purpose

Specify the repository's contributor workflow, hermetic Bazel toolchain, local Make entrypoints, and CI-equivalent verification surfaces.

## Requirements

### Requirement: Host tasks dispatch through hermetic Bazel targets

Top-level Make targets SHALL be the local and CI entrypoint for build, test, lint, formatting, vulnerability, mutation, integration, conformance, and release workflows. Make targets SHALL bootstrap repo-local Bazelisk and Go when needed, then delegate work to Bazel targets or Bazel test suites. Bazel SHALL resolve the Go SDK and Go module graph from repository manifests so routine workflows do not depend on host Go, Nix, or Task installs. Build and test actions SHALL declare their tools, inputs, environment, and execution requirements wherever technically possible so they remain compatible with sandboxing, remote caching, and remote execution; unavoidable host, kernel, network, or tracing exceptions SHALL be explicit and narrowly scoped.

#### Scenario: Tooling is provisioned under repo-local state

- **WHEN** a contributor runs `make bootstrap` or another Make target that depends on bootstrap
- **THEN** Bazelisk and the bootstrap Go binary are installed under `.local/tools`
- **AND** Bazelisk home, Bazel output user root, and Bazel disk cache stay under `.local/`
- **AND** no global package manager state is required for normal repository workflows

#### Scenario: Host runs a build workflow task

- **WHEN** a contributor runs `make build`
- **THEN** Make invokes Bazel over `BAZEL_BUILD_TARGETS`, defaulting to `//...`
- **AND** repository binaries and libraries build with the Bazel-resolved Go toolchain

#### Scenario: Host runs the pull-request CI pipeline locally

- **WHEN** a contributor runs `make ci-local`
- **THEN** Make builds Bazel target `//:ci-local`
- **AND** the runner invokes the repository-owned build, unit-test, race-test, conformance, lint, vulnerability, coverage-threshold, and GoReleaser-check commands used by the GitHub pull-request workflow
- **AND** the runner executes host-native against the current worktree because Bazel already provisions the repository toolchain hermetically

#### Scenario: Host runs a unit test workflow task

- **WHEN** a contributor runs `make test`
- **THEN** Make invokes Bazel over `BAZEL_TEST_TARGETS`, defaulting to `//:test`
- **AND** the default unit-test tag filter excludes integration, conformance, mutation, lint, manual, and external tests

#### Scenario: Host runs a formatting workflow task

- **WHEN** a contributor runs `make format`
- **THEN** Make invokes Bazel target `//:format`
- **AND** configured Go, Bazel/Starlark, YAML, shell, TOML, and Gazelle formatters run through Bazel-provisioned tools

#### Scenario: Host runs a lint workflow task

- **WHEN** a contributor runs `make lint`
- **THEN** Make invokes Bazel test suite `//:lint`
- **AND** configured linters, format checks, Gazelle/buildifier checks, file-coverage checks, secret scanning, and release-configuration validation run through Bazel targets without disabling strictness

#### Scenario: Host runs a vulnerability workflow task

- **WHEN** a contributor runs `make govulncheck`
- **THEN** Make invokes Bazel target `//:govulncheck`
- **AND** vulnerability scanning uses the repository's Bazel-provisioned Go vulnerability checker

#### Scenario: Host runs release workflow tasks

- **WHEN** a contributor runs `make release-check`, `make release-snapshot`, or `make release`
- **THEN** Make invokes the matching Bazel target for GoReleaser validation, snapshot release, or tagged release publication
- **AND** GoReleaser runs with Bazel-provisioned Go and release companion tools on `PATH`

### Requirement: Generated artifacts and caches are scoped to repository-owned directories

The development workflow SHALL keep repo-local tool state, Bazel caches, and Bazel convenience symlinks under `.local/`, and release output under `build/dist/` unless a caller explicitly overrides the documented Make variables.

#### Scenario: Bootstrap cache preamble runs

- **WHEN** a Make target requires the tool environment
- **THEN** `make bootstrap` prepares `.local/tools`, `.local/bazelisk`, `.local/bazel`, and `.local/bazel-disk-cache` as needed

#### Scenario: Clean is requested

- **WHEN** `make clean` runs
- **THEN** Bazel clean removes build outputs without deleting repo-local downloaded tools

#### Scenario: Full clean is requested

- **WHEN** `make clean-all` runs
- **THEN** Bazel clean runs first
- **AND** `.local/` is removed so the next workflow bootstraps fresh tool state

### Requirement: Formatting and linting are scoped to owned repository surfaces

Formatting and linting tasks SHALL operate on repository-owned Go, YAML, Markdown, shell, Starlark, TOML, workflow, spelling, module, and release-configuration surfaces while avoiding generated caches, Bazel outputs, release output, and third-party module sources. CI SHALL call the same Make targets that contributors run locally. Go lint analysis SHALL use the synchronized committed vendor tree with network access blocked so the Bazel test remains sandboxed and remote-execution eligible.

#### Scenario: Formatting runs

- **WHEN** `make format` runs
- **THEN** Go, Bazel/Starlark, YAML, shell, TOML, and Gazelle formatters operate on their configured source filegroups
- **AND** generated cache and output directories are excluded

#### Scenario: Format check runs in CI

- **WHEN** `make lint` runs in CI
- **THEN** formatter check targets fail if any owned file would change
- **AND** no lint target is disabled to hide formatting drift

#### Scenario: Lint runs

- **WHEN** `make lint` runs
- **THEN** `golangci-lint`, `yamllint`, `rumdl`, `shellcheck`, `typos`, `gitleaks`, `checkmake`, `goreleaser check`, `buildifier`, `gazelle`, Starlark checks, TOML checks, and file-coverage checks run through Bazel
- **AND** source file-coverage checks fail when a repository-owned Go, Markdown, Makefile, shell, Starlark, TOML, YAML, or workflow file is not included in the matching Bazel filegroup

### Requirement: Test workflows are tiered by cost and environment

The repository SHALL separate unit tests, conformance tests, integration tests, mutation testing, vulnerability scanning, and release validation behind distinct Make targets backed by Bazel suites or Bazel targets.

#### Scenario: Unit tests run

- **WHEN** `make test` runs
- **THEN** Bazel executes unit tests while excluding integration, conformance, mutation, lint, manual, and external tags by default

#### Scenario: Conformance tests run

- **WHEN** `make test-conformance` runs
- **THEN** Bazel executes `//:conformance` with the conformance configuration

#### Scenario: Coverage thresholds run

- **WHEN** `make test-coverage` runs
- **THEN** Bazel executes unit-test coverage and writes `build/coverage/coverage.lcov`
- **AND** the configured total and per-package thresholds in `.testcoverage.yaml` are enforced before the target succeeds

#### Scenario: Integration tests run directly

- **WHEN** `make test-integration` runs
- **THEN** Bazel executes `//:integration` with the integration configuration

#### Scenario: Mutation tests run

- **WHEN** `make test-mutation` runs
- **THEN** Bazel executes `//:mutations`
- **AND** the mutation tool is provided by the Bazel harness rather than a runtime `go install`

### Requirement: GitHub Actions use the Make/Bazel contract

GitHub Actions workflows SHALL invoke Make targets for repository build, test, lint, vulnerability, mutation, and release operations so CI behavior matches local contributor behavior. Kernel integration SHALL remain a manual Make target for capable Linux hosts rather than a GitHub Actions job.

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

- **WHEN** the release workflow runs for a stable SemVer tag
- **THEN** prereq jobs invoke the reusable Make/Bazel security, unit, conformance, coverage, and architecture/API gates
- **AND** the publish job invokes `make ci-local`, `make changelog`, and `make release`
- **AND** release publication uses the workflow `GITHUB_TOKEN` and GoReleaser environment expected by the Bazel release target
