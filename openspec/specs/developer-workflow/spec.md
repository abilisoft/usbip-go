## Purpose

Specify the repository's contributor workflow, hermetic toolchain, local task dispatch, and CI-equivalent verification surfaces.

## Requirements

### Requirement: Host tasks dispatch into the hermetic development environment
Top-level Taskfile targets SHALL dispatch through the Nix/Docker development environment unless the process is already inside the expected containerized devShell.

#### Scenario: Host runs a top-level task
- **WHEN** a contributor runs `task test`, `task lint`, `task fmt`, `task vuln`, `task build`, or another top-level workflow task from the host
- **THEN** the task uses the `_run` dispatcher to invoke the matching `ci:*` task inside `docker compose run --rm dev`

#### Scenario: Task already runs inside the dev container
- **WHEN** `IN_NIX_CONTAINER=1`, `/.dockerenv` exists, the working directory is `/src`, and `go` resolves under `/nix/store`
- **THEN** `_run` invokes the inner `ci:*` task directly without nesting another container

#### Scenario: Tooling version drifts
- **WHEN** `go.mod` or `toolchain` declares a different Go version than the devShell provides
- **THEN** `_check:tooling` fails before build, test, lint, or release commands run

### Requirement: Build artifacts and caches stay under build
The development workflow SHALL keep generated artifacts, identities, Go caches, lint caches, coverage output, VM closures, and release output under `build/`.

#### Scenario: Cache preamble runs
- **WHEN** any inner `ci:*` target depends on `_check:tooling`
- **THEN** `build/cache/go-build`, `build/cache/go-mod`, `build/cache/go-bin`, and `build/cache/golangci-lint` are created as needed

#### Scenario: Identity files are prepared
- **WHEN** `_prep:identity` runs
- **THEN** minimal passwd and group files are written under `build/cache`
- **AND** stale directory placeholders at those paths are removed first

#### Scenario: Clean is requested
- **WHEN** `task clean` runs
- **THEN** only first-level contents under `build/` are removed

### Requirement: Formatting and linting are scoped to owned source roots
Formatting and linting tasks SHALL operate on repository-owned Go package roots and avoid generated caches or third-party module sources under `build/cache`.

#### Scenario: Formatting runs
- **WHEN** `task fmt` dispatches to `ci:fmt`
- **THEN** `gofmt -s`, `gofumpt`, and `goimports` run over `cmd`, `examples`, `internal`, `pkg`, and `test`

#### Scenario: Format check runs in CI
- **WHEN** `ci:fmt:check` runs
- **THEN** `gofmt -s -l` reports drift over the owned source roots and fails if any file would change

#### Scenario: Lint runs
- **WHEN** `task lint` dispatches to `ci:lint`
- **THEN** `golangci-lint` runs over `cmd/...`, `pkg/...`, `internal/...`, `test/...`, and `examples/...`
- **AND** it does not recurse through `build/cache/go-mod`

### Requirement: Test workflows are tiered by cost and environment
The repository SHALL separate race-enabled unit tests, conformance tests, integration tests, coverage, mutation testing, and microVM-backed Linux integration.

#### Scenario: Unit tests run
- **WHEN** `task test` dispatches to `ci:test`
- **THEN** `go test -race -timeout=180s` runs over `cmd/...`, `pkg/...`, `internal/...`, and `test/...`

#### Scenario: Conformance tests run
- **WHEN** `task test:conformance` dispatches to `ci:test:conformance`
- **THEN** Go tests run with the `conformance_linux` build tag over `test/conformance/...`

#### Scenario: Integration tests run directly
- **WHEN** `task test:integration` dispatches to `ci:test:integration`
- **THEN** Go tests run with race detector and `integration_linux` build tag over `test/integration/...`

#### Scenario: Coverage report is requested
- **WHEN** `task test:cover` dispatches to `ci:test:cover`
- **THEN** race-enabled tests write `build/coverage/coverage.out`
- **AND** an HTML coverage report is written to `build/coverage/coverage.html`

#### Scenario: Mutation tests run
- **WHEN** `task test:mutation` dispatches to `ci:test:mutation`
- **THEN** gremlins is installed into `build/cache/go-bin`
- **AND** mutation runs against protocol-critical packages from a staged copy that excludes the repository's build cache

### Requirement: microVM workflow provides kernel-module integration coverage
The repository SHALL provide microVM tasks that materialize a pinned VM runner and execute USB/IP module smoke and integration tests in that VM.

#### Scenario: microVM closure is built
- **WHEN** `task vm:build` dispatches to `ci:vm:build`
- **THEN** the Nix `microvm-run` closure is materialized under `build/vm/run`

#### Scenario: microVM smoke runs
- **WHEN** `task vm:smoke` dispatches to `ci:vm:smoke`
- **THEN** the VM asserts that `usbip_core`, `vhci_hcd`, `usbip_host`, `usbip_vudc`, and `libcomposite` are loaded

#### Scenario: microVM integration runs
- **WHEN** `task vm:test:integration` dispatches to `ci:vm:test:integration`
- **THEN** host-side dependency resolution is prewarmed
- **AND** `go test -race -v -count=1 -buildvcs=false -tags=integration_linux` runs inside the VM over `test/integration/...`

### Requirement: Local GitHub Actions rehearsal is available
The Taskfile SHALL expose `act` targets for local workflow rehearsal outside the dev container.

#### Scenario: Contributor lists actions
- **WHEN** `task act:list` runs
- **THEN** `act -l push` lists jobs for the push event

#### Scenario: Contributor runs one action job
- **WHEN** `task act:job JOB=name` runs with a non-empty JOB
- **THEN** `act push -j name` runs that job locally

#### Scenario: Contributor omits job name
- **WHEN** `task act:job` runs without JOB
- **THEN** Taskfile preconditions fail with guidance to set `JOB=<name>`
