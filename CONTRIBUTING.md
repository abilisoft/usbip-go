# Contributing to usbip-go

Thanks for considering a contribution. This project follows a strict
TDD + DDD workflow; read the authoritative design spec and
implementation plan in
[`docs/superpowers/specs/`](docs/superpowers/specs/) and
[`docs/superpowers/plans/`](docs/superpowers/plans/) before opening a
PR.

## Prerequisites

- **Go 1.26+**.
- **[Task](https://taskfile.dev)** — install with
  `go install github.com/go-task/task/v3/cmd/task@latest`.
- **Dev tooling** — run `task install-tools` once. Installs
  `gofumpt`, `golangci-lint` (v2), `govulncheck`, `goreleaser`, `moq`,
  `gremlins`, `apidiff`, and `goimports` into `$GOBIN`. Versions are
  pinned via [`internal/tools/tools.go`](internal/tools/tools.go).
- **[git-cliff](https://git-cliff.org/)** — changelog generator, Rust
  binary, not managed by `install-tools`. Install the prebuilt binary
  from the
  [releases page](https://github.com/orhun/git-cliff/releases) or
  `cargo install git-cliff`. Required only when regenerating
  `CHANGELOG.md` via `task changelog`.

## Dev loop

```
task fmt      # gofumpt + goimports
task lint     # golangci-lint (must be "0 issues.")
task vuln     # govulncheck
task test     # -race unit tests
task build    # release-style build of usbip + usbipd
```

`task check` runs `fmt`, `lint`, `vuln`, `test` in sequence — the
minimum bar before pushing.

Integration and conformance suites live behind build tags so
ordinary `task test` stays fast:

```
task test:integration   # requires vhci-hcd + usbip-vudc on the host
task test:conformance   # requires upstream usbip-utils installed
task test:cover         # HTML coverage report
```

## TDD discipline

TDD is enforced mechanically by the `test-tdd-discipline` CI job for
every PR. The rule is:

- **Every implementation commit is preceded by a RED commit** that
  adds a failing test. The pair is easy to spot: the RED subject
  begins with `test(...)`; the GREEN subject follows as `feat(...)`
  or `fix(...)`.
- **Refactor-only commits** are labeled `refactor:` and contain no
  behaviour change. The CI job accepts `refactor:` subjects as a
  valid break in the RED → GREEN chain.
- A commit that follows a RED commit and adds no implementation
  without a `refactor:` label is rejected at PR review.

Spec §3 Compliance Gates 1-4 define the discipline; the CI workflow
enforces gates 1-6, 8, and 12 mechanically.

## Commit conventions

- [Conventional Commits](https://www.conventionalcommits.org):
  `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`, `ci:`,
  `build:`, `perf:`. Scope in parentheses is encouraged:
  `feat(app): ...`.
- Breaking changes to `pkg/usbip` or `pkg/domain` require a
  `BREAKING:` prefix on the relevant commit subject so the API-
  surface CI check acknowledges the break.
- `CHANGELOG.md` is generated from commit history by
  [`git-cliff`](https://git-cliff.org/) — **never hand-edit it**.
  Regenerate after your changes with `task changelog`. The CI
  `changelog-check` job (release tags only) diffs the checked-in
  file against what `git-cliff` would produce; a mismatch fails the
  build.

## Style rules

- Formatter: `gofumpt` + `goimports`. `task fmt` runs both.
- Linter: `golangci-lint` with the config at
  [`.golangci.yml`](.golangci.yml). `default: all` with a minimal,
  justified disable list.
- Code review applies spec §9 style rules — comments explain WHY,
  not WHAT; no `//nolint` without a cited rationale; no `t.Skip`
  without a tracked reason; magic numbers named.
- Lines are bounded at 120 chars (`lll` linter). Cyclomatic complexity
  capped at 10 (`cyclop`, `gocyclo`, `gocognit`).

## API-surface baselines

Stable public packages (`pkg/usbip` and `pkg/domain`) carry
[`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff)
baselines:

- `api/pkg_usbip.json`
- `api/pkg_domain.json`

The CI `api-surface` job diffs the baselines against the current
tree. Any incompatible change fails the build. When a PR
intentionally breaks either surface (subject line begins with
`BREAKING:`), regenerate the affected baseline in the same PR so the
next comparison starts from the new contract:

```
apidiff -w api/pkg_usbip.json  github.com/abilisoft/usbip-go/pkg/usbip
apidiff -w api/pkg_domain.json github.com/abilisoft/usbip-go/pkg/domain
```

Run both from the repository root. Commit the regenerated files
alongside the `BREAKING:`-prefixed change.

## Compliance gates

Every PR is validated against the 12 compliance gates defined in the
plan header. The CI workflow
([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) enforces
gates 1-6, 8, and 12 mechanically:

| Gate | What | Job |
|---|---|---|
| 1 | Lint clean | `lint-and-vet` |
| 2 | Vuln scan clean | `lint-and-vet` |
| 3 | RED→GREEN commit chain | `test-tdd-discipline` |
| 4 | Coverage thresholds | `coverage` |
| 5 | DDD layering (`pkg/` ↛ `internal/`; `internal/app` ↛ `internal/adapter`) | `ddd-boundary` |
| 6 | API-surface baselines | `api-surface` |
| 8 | No cgo | `no-cgo` |
| 12 | Cross-compile linux/{amd64,arm64,arm} | `cross-compile` |

The remaining gates (metrics catalog completeness, error-mapping
matrix, etc.) are code-review checklist items until a later phase
introduces the required infrastructure.

## Code-review checklist

When reviewing a PR, verify:

- [ ] Subject line is a valid Conventional Commit and matches the
      work done. `BREAKING:` is present when the API surface changes.
- [ ] A RED commit precedes every implementation commit, or the
      commit is labelled `refactor:`.
- [ ] New public API in `pkg/usbip` or `pkg/domain` has godoc on
      every exported identifier.
- [ ] Tests cover happy path and at least one failure mode per new
      branch.
- [ ] `task lint` reports `0 issues.` locally.
- [ ] `task test` is race-clean.
- [ ] No `//nolint` without a rule + rationale comment that cites
      the spec section or linter rule.
- [ ] No `t.Skip` without an issue reference.
- [ ] Comments explain WHY, not WHAT.
- [ ] `.github/pull_request_template.md` sections are filled in.
- [ ] `BREAKING:` changes include regenerated `api/*.json`
      baselines.
- [ ] `CHANGELOG.md` regeneration is not required mid-PR; the
      release workflow runs `task changelog` at tag time.

## Reporting bugs

For protocol-path bugs, attach the five artefacts listed in
[`docs/wire-trace.md`](docs/wire-trace.md) — pcap, trace-level
daemon log, `usbipd version`, status-socket snapshot, relevant
`dmesg`. With those, most reports are reproducible from first
principles.

For everything else, include:

- Exact `usbip` / `usbipd` version (`usbipd version`).
- Kernel version (`uname -r`) and loaded module versions
  (`modinfo usbip_host`, etc.).
- Full error output, ideally from a `--log-level=trace` run.
- Steps to reproduce.
