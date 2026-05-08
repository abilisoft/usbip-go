# Contributing to usbip-go

Thanks for considering a contribution. This project follows a strict TDD and
DDD workflow; please read the design spec and implementation plan in
[`docs/superpowers/specs/`](docs/superpowers/specs/) and
[`docs/superpowers/plans/`](docs/superpowers/plans/) before opening a PR.

## Prerequisites

- **Go 1.26+**.
- **[Task](https://taskfile.dev)** — install with
  `go install github.com/go-task/task/v3/cmd/task@latest`.
- Dev tooling — run `task install-tools` once to install
  `gofumpt`, `golangci-lint` (v2), `govulncheck`, `goreleaser`, `moq`,
  `gremlins`, `apidiff`, and `goimports` into your `$GOBIN`. All are
  pinned via [`internal/tools/tools.go`](internal/tools/tools.go).
- **[git-cliff](https://git-cliff.org/)** (changelog generator, Rust
  binary, not managed by `install-tools`). Install by downloading the
  prebuilt binary from the
  [releases page](https://github.com/orhun/git-cliff/releases) or via
  `cargo install git-cliff`. Required only when regenerating
  `CHANGELOG.md` via `task changelog`.

## Commit conventions

- Commits follow [Conventional Commits](https://www.conventionalcommits.org):
  `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`, `ci:`, `build:`,
  `perf:`.
- TDD: a failing-test commit (`test(...):`) precedes the implementation
  commit (`feat(...):` or `fix(...):`). A refactor-only commit uses
  `refactor:` and contains no behaviour change.
- Breaking changes to `pkg/usbip` or `pkg/domain` require a `BREAKING:`
  prefix on the relevant commit subject so the API-surface CI check can
  acknowledge the break.
- `CHANGELOG.md` is generated from commit history by
  [`git-cliff`](https://git-cliff.org/) — **never hand-edit it**.
  Regenerate after your changes with `task changelog`. The CI
  `changelog-check` job diffs the checked-in file against what
  `git-cliff` would produce; a mismatch fails the build.

## Compliance gates

Every PR is validated against the 12 compliance gates defined in the plan
header. The CI workflow (`.github/workflows/ci.yml`) enforces gates 1-6,
8, and 12 mechanically. The remaining gates are code-review checklist
items and will become mechanical as later phases introduce the required
infrastructure (metrics catalog, error-mapping matrix, etc.).
