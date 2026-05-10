# Contributing to usbip-go

Thanks for considering a contribution. This project follows a strict
TDD + DDD workflow. Read [`docs/architecture.md`](docs/architecture.md)
and [`docs/protocol.md`](docs/protocol.md) for the layering and
wire-format reference, then run the test suite locally (`task test`)
before opening a PR.

## Prerequisites

The toolchain is hermetic and split by cost. Go build/test tools,
lint/format/analyzer tools, release tools, and microVM tools are pinned in
[`flake.nix`](flake.nix) and run through Nix containers. The fast `dev` shell
carries build/test tooling; `qa` carries lint, format, spelling, vulnerability,
and release-config validation tooling; `release` carries GoReleaser,
SBOM/signing/package/changelog tools; `vm` carries the microVM runner. The
only host-side dependencies are:

- **Docker Engine 20.10+** (or a compatible daemon exposing the
  `docker` CLI and `docker compose`). On Linux install via your
  distro's package manager; on macOS use
  [Docker Desktop](https://www.docker.com/products/docker-desktop/).
- **[Task](https://taskfile.dev)** — install with
  `go install github.com/go-task/task/v3/cmd/task@latest`, or via
  `brew install go-task` on macOS, or any of the options on the
  Taskfile install page.

That is everything. No host Go, no host linters/formatters, no host
GoReleaser; the flake shells provide all of them.

### Bootstrap

You do not need to run setup manually for normal use. Top-level tasks seed the
smallest required Docker/Nix shell before executing:

- `task test`, `task build`, `task tidy` → `.#dev`
- `task lint`, `task fmt`, `task vuln`, `task check` → `.#qa`
- `task release:notes`, `task release:snapshot`, `task release` → `.#release`
- `task vm:build`, `task vm:smoke`, `task vm:test:integration` → `.#vm`

If you want to prewarm or debug a shell explicitly, use `task setup:dev`,
`task setup:qa`, `task setup:release`, `task setup:vm`, `task shell`,
`task shell:qa`, `task shell:release`, or `task shell:vm`.

The volume name includes your UID, GID, and a sha256 prefix of the absolute
workspace path (see `docker volume ls`), so multiple checkouts never alias into
one store. To share one store across workspaces, export
`USBIP_GO_NIX_VOLUME=<your-chosen-name>` before running any `task` command.

If the store ever ends up in a bad state (interrupted setup, manual
`docker volume` edit, a flake pin that produced broken derivations), reset it:

```text
docker compose down -v      # removes the named volume
task clean                  # clears build/ caches
task setup:dev              # re-seeds the fast build/test shell
```

## Dev loop

```text
task fmt      # gofmt/gofumpt/goimports + yamlfmt + rumdl + shfmt + nixpkgs-fmt + taplo
task lint     # Go/YAML/Actions/Markdown/shell/spelling/Nix/TOML/Compose/OpenSpec/release lint gates
task vuln     # govulncheck
task test     # -race unit tests
task build    # release-style build of usbip-go → build/bin/
```

`task check` runs `fmt`, `tidy:check`, `lint`, `vuln`, `test` in sequence — the
minimum bar before pushing. All build artefacts land under
`./build/` (binaries in `build/bin/`, coverage under
`build/coverage/`, goreleaser output under `build/dist/`, and every
cache — Go, golangci-lint, home — under `build/cache/`). `task
clean` removes generated build contents.

Integration and conformance suites live behind build tags so
ordinary `task test` stays fast:

```text
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

```text
task vm:build               # build the kernel + initrd + runner closure
task vm:smoke               # boot, assert modules load, power off
task vm:test:integration    # run ./test/integration/... inside the VM
```

The microVM needs `/dev/kvm` on the host for acceptable speed —
KVM gives ~15 s end-to-end, TCG fallback (opt-in via
`USBIP_GO_VM_ALLOW_TCG=1`) is ~70 s. The default compose path
unconditionally maps `/dev/kvm` into the dev service; Docker
Desktop on macOS does not expose `/dev/kvm`, so `task vm:*` is not
supported there — use a Linux host (including a Linux VM on macOS).
The integration tier IS run by GitHub Actions via
[`vm-integration.yml`](.github/workflows/vm-integration.yml): the
workflow boots the microVM with `accel=kvm:tcg` so it picks KVM
on a self-hosted runner and falls back to TCG software emulation
on hosted ubuntu-24.04 (which no longer exposes `/dev/kvm` on the
standard SKU). TCG is significantly slower (~30-60 min sweep vs
10-15 min on KVM); the workflow's daily cron + the broadened PR
`paths:` filter (cmd/, internal/, pkg/, go.{mod,sum}) means a
production-source PR will exercise the kernel surface within the
job's 90-minute timeout, just slowly. Run `task vm:test:integration`
locally on a KVM-capable Linux host for fast feedback before
pushing.

## TDD discipline

TDD is enforced mechanically by the `TDD commit discipline` job in
`ci.yml` (PR events only — pushes to main do not re-evaluate the
RED→GREEN chain). The rule the gate enforces is the
"incomplete-feat/fix" check, not strict test-first:

- **A `feat:`/`fix:` commit that adds at least one new
  `*_test.go` and touches no non-test `.go` outside
  `internal/tools/` is treated as RED** — an unfinished
  behaviour-introducing commit. The very next commit MUST touch
  a non-test `.go` outside `internal/tools/` (additions OR
  modifications both count — the gate uses git diff-tree, which
  reports any change) OR be a `refactor:`-prefixed commit (the
  only accepted break in the chain). Anything else
  (`docs:`/`chore:`/`ci:`/`build:`/`perf:`/`style:`) leaves the
  RED unfollowed and fails the gate.
- **`test:` commits do NOT carry RED forward.** In this codebase
  `test(scope):` means "tests for already-shipped code" —
  coverage hardening / mutation gap closure / pinning a
  contract — not strict-TDD test-first. The gate explicitly
  ignores `test:`-prefixed commits when deciding whether the
  next commit must touch prod code.
- **A trailing dangling RED at the end of the PR fails the gate**
  so an unfollowed `feat:`-only-tests commit cannot merge.
- **Refactor commits** are accepted by subject prefix
  (`refactor(scope):`); reviewers verify they ship no behaviour
  change. They satisfy the post-RED slot and are also valid on
  their own.

Strict test-first writers preferring `test(...)` → `feat(...)`
pairs SHOULD still keep them adjacent in the same PR; the gate
won't reject the test-first commit on its own, but reviewers will
flag a stale `test:` commit at PR review.

The OpenSpec developer-workflow and security/release capabilities
define the discipline; the CI workflow enforces gates 1-6, 8, and
12 mechanically.

## Commit conventions

- [Conventional Commits](https://www.conventionalcommits.org):
  `feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`, `ci:`,
  `build:`, `perf:`. Scope in parentheses is encouraged:
  `feat(app): ...`.
- Breaking changes to `pkg/usbip` or `pkg/domain` require a
  `BREAKING:` prefix on the relevant commit subject so the API-
  surface CI check acknowledges the break.
- Release notes live on the [GitHub Releases
  page](https://github.com/abilisoft/usbip-go/releases), generated at
  tag time by [`git-cliff`](https://git-cliff.org/) from the commit
  history since the previous tag and published by goreleaser as the
  release body. There is no checked-in `CHANGELOG.md` to maintain —
  the commit log is the source of truth, so well-formed Conventional
  Commit subjects are what land in the rendered notes.

## Style rules

- Formatters: `gofmt -s`, `gofumpt`, `goimports`, `yamlfmt`,
  `rumdl fmt`, `shfmt`, `nixpkgs-fmt`, and `taplo fmt`. `task fmt`
  runs all of them.
- Linters: `golangci-lint` with the config at
  [`.golangci.yml`](.golangci.yml), plus `yamllint`, `actionlint`,
  `rumdl check`, `shellcheck`, `typos`, `goreleaser check`,
  `statix`, `deadnix`, `taplo lint`, `docker compose config`, and
  `openspec validate --specs --strict`.
  `golangci-lint` uses `default: all` with a minimal, justified
  disable list.
- Code review applies the repository style rules — comments explain
  WHY, not WHAT; no `//nolint` without a cited rationale; no
  `t.Skip` without a tracked reason; magic numbers named.
- Lines are bounded at 120 chars (`lll` linter). Cyclomatic complexity
  capped at 10 (`cyclop`, `gocyclo`, `gocognit`).

## API-surface baselines

Stable public packages (`pkg/usbip` and `pkg/domain`) carry
[`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff)
baselines:

- `api/pkg_usbip.json`
- `api/pkg_domain.json`

The CI `api-compatibility` job (in `_arch-checks.yml`) diffs the baselines against the current
tree. Any incompatible change fails the build. When a PR
intentionally breaks either surface (subject line begins with
`BREAKING:`), regenerate the affected baseline in the same PR so the
next comparison starts from the new contract:

```text
apidiff -w api/pkg_usbip.json  github.com/abilisoft/usbip-go/pkg/usbip
apidiff -w api/pkg_domain.json github.com/abilisoft/usbip-go/pkg/domain
```

Run both from the repository root. Commit the regenerated files
alongside the `BREAKING:`-prefixed change.

## Compliance gates

Every PR is validated against the compliance gates summarized below
and backed by OpenSpec's developer-workflow and security/release
capabilities. The CI workflow
([`.github/workflows/ci.yml`](.github/workflows/ci.yml)) enforces
gates 1-6, 8, and 12 mechanically; the remainder are code-review
items per the progressive-enforcement policy:

| Gate | What | Enforcement |
|---|---|---|
| 1 | `task lint` clean (Go, YAML, GitHub Actions, Markdown, shell, spelling, Nix, TOML, Compose, OpenSpec, and release config checks) plus formatter drift checks (`gofmt`, `yamlfmt`, `rumdl`, `shfmt`, `nixpkgs-fmt`, `taplo`) | CI: `Format, lint, and vulnerability scan` (in `_security.yml`). |
| 2 | `task test` clean with `-race` on linux | CI: `Linux unit tests` (in `_unit-tests.yml`). A separate `USB/IP wire conformance` job (in `_conformance.yml`) runs `task ci:test:conformance`; upstream-binary cross-checks inside it skip when `usbip` is not on PATH (the flake closure does not pin usbip-utils). |
| 3 | RED→GREEN commit chain — every `feat:`/`fix:` commit that adds at least one new `*_test.go` and touches no non-test `.go` outside `internal/tools/` is immediately followed by a commit that touches non-test `.go` outside `internal/tools/` (the GREEN; additions OR modifications both count) OR by a `refactor:` commit (the only accepted break). `test:`-prefixed commits are NOT carried as RED (treated as coverage hardening). Trailing dangling RED at the PR tip also fails. | CI: `TDD commit discipline` job in `ci.yml` (PRs only). |
| 4 | Coverage thresholds per `.testcoverage.yaml` — per-package floor 80%, total 90% (the achievable floor across the kernel-adapter errno tail and the cmd Cobra-Action surface; pure-logic packages — `pkg/domain`, `pkg/usbip`, `internal/app`, `internal/adapter/wire` — clear 90% comfortably under the current test surface). | CI: `Coverage thresholds` (in `_coverage.yml`) runs `task test:cover` + `vladopajic/go-test-coverage` against `.testcoverage.yaml`. |
| 5 | DDD layering: `pkg/domain` ↛ `internal/`; `pkg/domain` is pure-stdlib (no third-party imports); `internal/app` ↛ `internal/adapter/{kernel,transport}` (wire is allowed because codec value types appear on app interface signatures). `pkg/usbip` is the public facade and intentionally imports `internal/*` to compose defaults. | CI: `Domain boundary rules` (in `_arch-checks.yml`) greps internal-import direction + uses `go list` to enumerate every third-party import in `pkg/domain`. |
| 6 | Public API stability for `pkg/usbip` + `pkg/domain`; breaking changes require a `BREAKING:` commit prefix | CI: `API compatibility` (in `_arch-checks.yml`) diffs against `api/pkg_usbip.json` + `api/pkg_domain.json` via `apidiff`; the BREAKING-prefix scan walks `merge-base..HEAD` on PR events. |
| 7 | No magic values (named constants only) | Code review (enforced indirectly by `mnd` + `goconst` in `task lint`, so rides Gate 1). |
| 8 | No cgo anywhere in the tree | CI: `Pure Go enforcement` (in `_arch-checks.yml`) uses `go list -f '{{.CgoFiles}}'` + source greps for `import "C"`. |
| 9 | Structured logging: `slog.DebugContext` + `oops.With(...)`, stable attr keys aligned with `openspec/specs/operations-observability/spec.md` | Code review (enforced indirectly by `sloglint` in `task lint`, so rides Gate 1). |
| 10 | Observability updates: new app side-effects add or reuse stable `outcome` values and update OpenSpec/docs/tests in the same PR | Code review. |
| 11 | Error mapping: new sysfs/wire paths map to public domain sentinels and OpenSpec error behavior in the same PR | Code review. |
| 12 | Cross-compile for `linux/{amd64,arm64,arm}` | CI: `Linux cross-compilation` (in `_arch-checks.yml`); release builds use `goreleaser build --snapshot` wiring. |

The `Format, lint, and vulnerability scan` job runs formatter drift
checks, the full lint suite, and `task vuln` (govulncheck), so config,
docs, shell, release metadata, and vulnerability scanning ride the
same required gate.

## Code-review checklist

When reviewing a PR, verify:

- [ ] Subject line is a valid Conventional Commit and matches the
      work done. `BREAKING:` is present when the API surface changes.
- [ ] A `feat:`/`fix:` commit that adds new `*_test.go` and
      touches no non-test `.go` outside `internal/tools/` is
      immediately followed by a commit that touches non-test
      `.go` outside `internal/tools/` (the GREEN; additions OR
      modifications both count) OR by a `refactor:` commit.
      `test:`-prefixed commits that add coverage for already-
      shipped code do NOT need a following GREEN — they are not
      carried forward by the gate.
- [ ] New public API in `pkg/usbip` or `pkg/domain` has godoc on
      every exported identifier.
- [ ] Tests cover happy path and at least one failure mode per new
      branch.
- [ ] `task lint` reports clean Go/YAML/Actions/Markdown/shell/spelling/release-config checks locally.
- [ ] `task test` is race-clean.
- [ ] No `//nolint` without a rule + rationale comment that cites
      the spec section or linter rule.
- [ ] No `t.Skip` without an issue reference.
- [ ] Comments explain WHY, not WHAT.
- [ ] `.github/pull_request_template.md` sections are filled in.
- [ ] `BREAKING:` changes include regenerated `api/*.json`
      baselines.
- [ ] Conventional Commit subjects are accurate; the GitHub Release
      body is generated from them at tag time, so a sloppy subject
      lands verbatim in user-visible release notes.

## Running CI locally

Most CI workflows under [`.github/workflows/`](.github/workflows/)
are replayable on a developer host via
[`act`](https://github.com/nektos/act), which executes each job
inside Docker containers shaped like the GitHub-hosted runners.
`task act:*` must run from the host shell — not from inside the dev
container — because act drives docker-compose itself and the dev
container does not mount `docker.sock`. Install `act` via your
package manager or GitHub releases, then run:

```text
task act:list                    # list jobs act would run for push
task act:job JOB=security        # run one job
task act:push                    # run every replayable push-event job
```

The repo-level `.actrc` pins the runner image to
`catthehacker/ubuntu:act-latest` and architecture to `linux/amd64`.

What does NOT replay locally:

- Integration suite — runs the hermetic microVM defined in
  `flake.nix`. The `vm-integration.yml` workflow DOES run it on
  GitHub Actions (via TCG fallback when `/dev/kvm` is absent), but
  `act` cannot replay it locally because the act runner image
  doesn't expose `/dev/kvm` either AND nesting qemu inside the
  act container is finicky. For fast iteration, run
  `task vm:test:integration` on the host (KVM-capable Linux).
- `release` (release.yml) — fires only on a stable SemVer-triple tag
  push (`vMAJOR.MINOR.PATCH`, no pre-release suffix and no build
  metadata; see the workflow's `on:tags` filter and `cliff.toml`'s
  matching anchor). Requires GitHub OIDC for cosign keyless signing
  and an authenticated `GITHUB_TOKEN`. GoReleaser will fail under
  act because the secrets do not exist.

There is no GitHub Actions step that invokes `act` — these targets
exist purely to short-circuit CI iteration.

## Reporting bugs

For protocol-path bugs, attach the five artefacts listed in
[`docs/wire-trace.md`](docs/wire-trace.md) — pcap, trace-level
daemon log, `usbip-go version`, status-socket snapshot, relevant
`dmesg`. With those, most reports are reproducible from first
principles.

For everything else, include:

- Exact `usbip-go` version (`usbip-go version`).
- Kernel version (`uname -r`) and loaded module versions
  (`modinfo usbip_host`, etc.).
- Full error output, ideally from a `--log-level=trace` run.
- Steps to reproduce.
