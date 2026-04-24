# Contributing to usbip-go

Thanks for considering a contribution. This project follows a strict
TDD + DDD workflow; read the authoritative design spec and
implementation plan in
[`docs/superpowers/specs/`](docs/superpowers/specs/) and
[`docs/superpowers/plans/`](docs/superpowers/plans/) before opening a
PR.

## Prerequisites

The dev environment is hermetic: every tool (Go, golangci-lint,
gofumpt, govulncheck, goreleaser, syft, cosign, nfpm, git-cliff, gh,
moq, gotools) is pinned in [`flake.nix`](flake.nix) and delivered
via a Nix container. The only host-side dependencies are:

- **Docker Engine 20.10+** (or a compatible daemon exposing the
  `docker` CLI and `docker compose`). On Linux install via your
  distro's package manager; on macOS use
  [Docker Desktop](https://www.docker.com/products/docker-desktop/).
- **[Task](https://taskfile.dev)** — install with
  `go install github.com/go-task/task/v3/cmd/task@latest`, or via
  `brew install go-task` on macOS, or any of the options on the
  Taskfile install page.

That is everything. No host Go, no host golangci-lint, no host
goreleaser; the flake closure provides all of them.

### Bootstrap

Run `task setup` once per checkout (and again whenever `flake.lock`
changes). That seeds a `/nix` Docker volume with the full closure
pulled from `cache.nixos.org` and hands its ownership to your host
UID/GID so subsequent `task *` invocations can acquire the store
lock without escalating privileges.

The volume name includes your UID, GID, and a sha256 prefix of the
absolute workspace path (see `docker volume ls`), so multiple
checkouts never alias into one store. To share one store across
workspaces, export `USBIP_GO_NIX_VOLUME=<your-chosen-name>` before
running any `task` command.

Subsequent commands (`task test`, `task lint`, etc.) reuse the same
volume, re-entering the hermetic shell automatically. If you ever
want a REPL inside it, `task shell` drops you into an interactive
`nix develop` session.

If the store ever ends up in a bad state (interrupted setup, manual
`docker volume` edit, a flake pin that produced broken derivations),
reset it:

```
docker compose down -v      # removes the named volume
task clean                  # clears build/ caches
task setup                  # re-seeds from scratch
```

## Dev loop

```
task fmt      # gofumpt + goimports
task lint     # golangci-lint (must be "0 issues.")
task vuln     # govulncheck
task test     # -race unit tests
task build    # release-style build of usbip + usbipd → build/bin/
```

`task check` runs `fmt`, `lint`, `vuln`, `test` in sequence — the
minimum bar before pushing. All build artefacts land under
`./build/` (binaries in `build/bin/`, coverage under
`build/coverage/`, goreleaser output under `build/dist/`, and every
cache — Go, golangci-lint, home — under `build/cache/`). `task
clean` removes everything except the tracked `build/go.mod` marker.

Integration and conformance suites live behind build tags so
ordinary `task test` stays fast:

```
task test:conformance   # runs wire/byte comparisons; upstream-binary
                        # checks inside it skip when `usbip` is not on
                        # PATH (it is not in the flake closure)
task test:cover         # HTML coverage report under build/coverage/
```

### Integration tests (microVM)

Integration tests need a live Linux kernel with `usbip_core`,
`vhci_hcd`, `usbip_host`, `usbip_vudc`, and `libcomposite` loaded.
Rather than demanding those modules on every contributor's host,
the flake builds a hermetic microVM:

```
task vm:build               # build the kernel + initrd + runner closure
task vm:smoke               # boot, assert modules load, power off
task vm:test:integration    # run ./test/integration/... inside the VM
```

The microVM needs `/dev/kvm` on the host for acceptable speed —
KVM gives ~15 s end-to-end, TCG fallback (opt-in via
`USBIP_GO_VM_ALLOW_TCG=1`) is ~70 s. The default compose path
unconditionally maps `/dev/kvm` into the dev service; Docker
Desktop on macOS does not expose `/dev/kvm`, so `task vm:*` is not
supported there — use a Linux host (including a Linux VM on macOS)
or wait for CI, which runs the integration tier on a self-hosted
`kvm`-labelled runner.

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
gates 1-6, 8, and 12 mechanically; the remainder are code-review
items per the plan's progressive-enforcement policy (Task 0.7 Step 8):

| Gate | What | Enforcement |
|---|---|---|
| 1 | `task lint` clean (gofumpt, wsl_v5, mnd, goconst, nolintlint, complexity ≤ 10, etc.) | CI: `lint-and-vet` job runs `task lint`. |
| 2 | `task test` clean with `-race` on linux + macos | CI: `unit-linux` + `unit-macos` jobs run `task test`. A dedicated `conformance` job additionally runs `task test:conformance` against upstream usbip-utils. |
| 3 | RED→GREEN commit chain (every `*_test.go`-adding commit is followed by implementation or a `refactor:` commit) | CI: `test-tdd-discipline` job on pull requests. |
| 4 | Coverage thresholds per §8.7 (domain 95, app 90, wire 95, kernel 70, transport 80, cmd 60) | CI: `coverage` job runs `task test:cover` + `vladopajic/go-test-coverage` against `.testcoverage.yaml`. |
| 5 | DDD layering: `pkg/` ↛ `internal/`; `internal/app` ↛ `internal/adapter/` | CI: `ddd-boundary` job greps both directions. |
| 6 | Public API stability for `pkg/usbip` + `pkg/domain`; breaking changes require a `BREAKING:` commit prefix | CI: `api-surface` job diffs against `api/pkg_usbip.json` + `api/pkg_domain.json` via `apidiff`. |
| 7 | No magic values (named constants only) | Code review (enforced indirectly by `mnd` + `goconst` in `task lint`, so rides Gate 1). |
| 8 | No cgo anywhere in the tree | CI: `no-cgo` job uses `go list -deps` + source greps for `import "C"`. |
| 9 | Structured logging: `slog.DebugContext` + `oops.With(...)`, stable attr keys per §11.5.5 | Code review (enforced indirectly by `sloglint` in `task lint`, so rides Gate 1). |
| 10 | Metrics registration: new app side-effects register a §11.5.5 catalog entry in the same PR | Code review. |
| 11 | Error mapping: new sysfs/wire paths map to the spec §6.2 + §6.4 sentinels in the same PR | Code review. |
| 12 | Cross-compile for `linux/{amd64,arm64,arm}` | CI: `cross-compile` job (Phase 0 minimal builds; switches to `goreleaser build --snapshot` when release wiring lands). |

Two additional CI jobs run outside the numbered-gate table:
`lint-and-vet` also runs `task vuln` (govulncheck) on every PR, and
`changelog-check` verifies `CHANGELOG.md` matches `git-cliff` output
on release tags.

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
