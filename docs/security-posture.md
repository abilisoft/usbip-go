# Supply-chain security posture

Operator-facing summary of every lever the repo and the release
pipeline pull to satisfy [OpenSSF Scorecard][scorecard] and
[OpenSSF Best Practices][bp]. This doc complements
[`docs/security.md`](security.md), which covers the *protocol-level*
security model (USB/IP is plaintext; deploy on trusted networks
only).

## What the repo does on its own

| Check                  | How                                                                                                              |
| ---------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Pinned-Dependencies    | Every workflow `uses:` is a 40-char SHA with a trailing `# vN` Dependabot anchor.                                |
| SAST                   | `.github/workflows/codeql.yml` runs CodeQL `security-and-quality` on every push/PR + weekly cron.                |
| Lint / config hygiene  | CI runs formatter drift, `go mod tidy` drift, Go, YAML, GitHub Actions, Markdown, shell, spelling, Nix, TOML, Compose, OpenSpec, and GoReleaser config checks. |
| Vulnerabilities        | `task vuln` / govulncheck on every push, PR, and nightly schedule.                                               |
| Token-Permissions      | Every workflow declares minimal top-level `permissions:`; jobs widen only when required (release / scorecard).   |
| Security-Policy        | [`SECURITY.md`](../SECURITY.md) at repo root.                                                                    |
| Signed-Releases        | GoReleaser + cosign keyless via GitHub OIDC; SBOM via syft. See `.goreleaser.yml`.                               |
| Dependency-Update-Tool | Dependabot weekly bumps for `gomod` + `github-actions`. See `.github/dependabot.yml`.                            |
| Fuzzing                | `internal/adapter/wire/fuzz_test.go` — codec fuzz targets seeded with historical malformed inputs.               |
| Maintained             | At least one commit per month while v1 is the active line.                                                       |
| License                | Apache-2.0; SPDX header on every source file.                                                                    |
| Binary-Artifacts       | No tracked binaries.                                                                                             |
| Dangerous-Workflow     | No `pull_request_target` with checkout; no untrusted-input substitution into `run:` blocks.                      |
| Scorecard analysis     | `.github/workflows/scorecard.yml` runs `ossf/scorecard-action` weekly + on push; result on https://scorecard.dev. |

## What the project owner enables on github.com

These two Scorecard checks require server-side configuration. The
repo content cannot enable them on its own.

1. **Repository ruleset / Branch-Protection** on `main`:
   - Require pull request reviews (1+).
   - Require status checks to pass. The current required contexts are:
     `Security / Format, tidy, lint, and vulnerability scan`,
     `Unit / Linux unit tests`, `Conformance / USB/IP wire
     conformance`, `Coverage / Coverage thresholds`, `Architecture /
     Domain boundary rules`, `Architecture / Pure Go enforcement`,
     `Architecture / API compatibility`, `Architecture / Linux
     cross-compilation`, `TDD commit discipline`, `CodeQL Go analysis`,
     `Trivy filesystem vulnerability scan`, `OpenSSF Scorecard
     analysis`, `Analyze (go)`, `CodeQL`, `Trivy`, `govulncheck`, and
     `codecov/patch`.
   - Require branches to be up to date before merging.
   - Require signed commits.
   - Restrict who can push to matching branches (admins only;
     everything else goes through PR).

2. **Code-Review** is satisfied by the PR-required configuration
   above plus an approving review.

Recommended GitHub settings:

```
Settings → Rules → Rulesets → Add branch ruleset for the default branch:
  ✓ Require a pull request before merging
    ✓ Require approvals (1)
    ✓ Dismiss stale pull request approvals when new commits are pushed
  ✓ Require status checks to pass
    Required (reusable-workflow callees show as "<caller> / <callee>"):
      Security / Format, tidy, lint, and vulnerability scan
      Unit / Linux unit tests
      Conformance / USB/IP wire conformance
      Coverage / Coverage thresholds
      Architecture / Domain boundary rules
      Architecture / Pure Go enforcement
      Architecture / API compatibility
      Architecture / Linux cross-compilation
      TDD commit discipline
      CodeQL Go analysis
      Trivy filesystem vulnerability scan
      OpenSSF Scorecard analysis
      Analyze (go)
      CodeQL
      Trivy
      govulncheck
      codecov/patch
  ✓ Require signed commits
  ✓ Require linear history
  ✓ Block force pushes
```

## OpenSSF Best Practices badge

The repo holds the **Passing** tier badge. The **Silver** and **Gold** tiers add criteria like multiple active
committers, a documented governance model, and a third-party
security audit on the change history. Silver is realistic post-1.0
once a second maintainer joins; Gold is a multi-quarter ask and is
not a v1 goal.

## Where the score caps

Without help from a second maintainer, two Scorecard checks are
inherently capped:

- **Contributors** rewards commits from ≥3 distinct organisations.
  A solo-author repo scores 0 here regardless of code quality.
- **Code-Review** rewards merged PRs with approving reviews. A
  solo author can only satisfy this by working on branches and
  self-merging via a PR (still records "approval = 0" on Scorecard
  v5).

Adding a co-maintainer raises both checks to full credit. Both are
people problems, not code problems.

## Packaging detection — install-only goreleaser-action step

Scorecard's Packaging check is static workflow-file analysis. Its
matcher for Go projects (see ossf/scorecard
`checks/fileparser/github_workflow.go::IsPackagingWorkflow`) ONLY
recognises a literal `uses: goreleaser/goreleaser-action` step.
Shell-wrapped invocations such as `nix develop .#release --command task
ci:release` are invisible to the matcher even when they execute the
same goreleaser binary.

`release.yml` reconciles two competing requirements:

1. **Hermetic release artefacts**: goreleaser, syft, cosign, and
   nfpm all come from the release flake shell (`flake.lock`), which
   pins the exact source revision of every tool. Local runs via
   `task release:snapshot` use the same binaries.
2. **Honest Scorecard signal**: the score should reflect what we
   actually ship.

The compromise is an explicit `goreleaser/goreleaser-action@<sha>`
step with `install-only: true` placed before the canonical release
step. The action installs a goreleaser binary onto runner PATH;
that binary is then SHADOWED by `nix develop .#release`'s PATH in the next
step, so the actual release work runs the flake-pinned binary, not
the action-installed one. The action-installed binary is never
executed at runtime, so its version does not need to track
`pkgs.goreleaser` in flake.nix — the action's `version:` input
uses a major-only constraint to reject accidental cross-major
jumps without requiring lock-step bumps.

The Packaging score moves from -1 to a positive value only after
the first successful run of `release.yml` (Scorecard requires both
the matched workflow file AND a green run for that file). The
score therefore lifts on the first stable SemVer triple
(`vMAJOR.MINOR.PATCH`, no pre-release or build-metadata suffix)
tag push that completes the release pipeline, not on the file
change alone. The trigger filter in `release.yml` and the
`tag_pattern` in `cliff.toml` are anchored to that exact shape so
pre-release / metadata tags do not fire the pipeline.

[scorecard]: https://github.com/ossf/scorecard
[bp]: https://www.bestpractices.coreinfrastructure.org/
