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

### Requirement: Two-guest KVM resilience validation is an explicit manual workflow

The repository SHALL expose production USB/IP validation across distinct
exporter and importer guest kernels through `make test-integration-vm`, backed
by one tracked Bazel local/manual/external target. The target SHALL declare
repository-owned scripts and fixtures while narrowly identifying KVM, QEMU,
cloud-image and SSH tooling, checksum-pinned image acquisition, and direct
inter-guest networking as non-hermetic host prerequisites. It SHALL cap each
guest at one vCPU and 1024 MiB, bound Bazel to one job, one CPU, 1024 MiB of
scheduled memory, and a 512 MiB host JVM, disable test-result caching, enforce
an overall deadline, and SHALL NOT run on standard GitHub-hosted automation.

#### Scenario: Two-guest KVM integration runs explicitly

- **WHEN** `make test-integration-vm` runs on a capable Linux KVM host
- **THEN** Bazel executes one tracked local/manual/external two-guest target with test-result caching disabled and an overall deadline
- **AND** the target builds the production binary through Bazel before booting distinct exporter and importer guests
- **AND** each guest uses one vCPU and 1024 MiB while Bazel uses one job, one scheduled CPU, 1024 MiB of scheduled memory, and a 512 MiB host JVM
- **AND** a dedicated direct inter-guest interface carries USB/IP while separate user-mode interfaces carry host SSH
- **AND** missing host tools, unusable KVM, image checksum failure, unavailable network acquisition, guest prerequisite failure, or a skipped test causes a non-zero result
- **AND** success requires both guest roles still running plus successful nonempty kernel, journal, system, and role-state evidence before log scanning
- **AND** run state and guest overlays are removed only after every guest is confirmed stopped and are preserved otherwise

#### Scenario: Standard hosted automation runs

- **WHEN** default, pull-request, nightly, or release automation runs on standard GitHub-hosted runners
- **THEN** it does not schedule the local/manual two-guest KVM target
- **AND** the absence of KVM or privileged guest-kernel facilities is not misreported as passing two-guest evidence

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

### Requirement: Git provenance checks declare their host dependency

Git provenance fixture tests SHALL resolve one exact Git executable, fail clearly when Git is unavailable, and declare the narrow host-tool execution requirement. The production release-stamping regression SHALL use committed constant status input instead of requiring Git inside its test action.

#### Scenario: Git provenance fixture tests run

- **WHEN** the version-helper or workspace-status fixture tests run
- **THEN** the Bazel targets are tagged `local` and `requires-git`
- **AND** each harness passes the resolved executable through `HARNESS_GIT`
- **AND** the release-stamping test remains sandboxable with constant workspace-status input
