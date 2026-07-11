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
| SAST                   | `.github/workflows/codeql.yml` traces the production Bazel binary through `make build-codeql`, then runs CodeQL `security-and-quality` on every push/PR + weekly cron. |
| Lint / config hygiene  | CI runs strict Bazel-backed formatter/lint drift checks for Go, Bazel, YAML, Markdown, shell, spelling, TOML, repository coverage, and GoReleaser config. |
| Vulnerabilities        | `make govulncheck` in PR, nightly, release, and local `make ci-local` gates; SARIF upload feeds code scanning.        |
| Token-Permissions      | Every workflow declares minimal top-level `permissions:`; jobs widen only when required (release / scorecard).   |
| Security-Policy        | [`SECURITY.md`](../SECURITY.md) at repo root.                                                                    |
| Signed-Releases        | GoReleaser + cosign keyless via GitHub OIDC; SBOM via syft. See `.goreleaser.yml`.                               |
| Dependency-Update-Tool | Dependabot weekly bumps for `gomod` + `github-actions`. See `.github/dependabot.yml`.                            |
| Fuzzing                | `internal/adapter/wire/fuzz_test.go` — codec fuzz targets seeded with historical malformed inputs.               |
| Maintained             | At least one commit per month while v1 is the active line.                                                       |
| License                | Apache-2.0; SPDX header on every source file.                                                                    |
| Binary-Artifacts       | No tracked binaries.                                                                                             |
| Dangerous-Workflow     | No `pull_request_target` with checkout; no untrusted-input substitution into `run:` blocks.                      |
| Scorecard analysis     | `.github/workflows/scorecard.yml` runs `ossf/scorecard-action` weekly + on push; result on <https://scorecard.dev>. |

## What the project owner enables on github.com

These two Scorecard checks require server-side configuration. The
repo content cannot enable them on its own.

1. **Repository ruleset / Branch-Protection** on `main`:
   - Require pull request reviews (1+).
   - Require status checks to pass. The current required contexts mirror the
     active `default-branches` repository ruleset: `Security / Format, tidy,
     lint, and vulnerability scan`, `Unit / Linux unit tests`, `Conformance /
     USB/IP wire conformance`, `Coverage / Coverage thresholds`, `Architecture /
     Domain boundary rules`, `Architecture / Pure Go enforcement`,
     `Architecture / API compatibility`, `Architecture / Linux
     cross-compilation`, `TDD commit discipline`, `CodeQL Go analysis`,
     `Analyze (go)`, `Trivy filesystem vulnerability scan`, `OpenSSF Scorecard
     analysis`, and the external app contexts `CodeQL`, `Trivy`, `govulncheck`,
     and `codecov/patch`.
   - Require branches to be up to date before merging.
   - Require signed commits.
   - Restrict who can push to matching branches (admins only;
     everything else goes through PR).

2. **Code-Review** is satisfied by the PR-required configuration
   above plus an approving review.

Recommended GitHub settings:

```text
Settings → Rules → Rulesets → Add branch ruleset for the default branch:
  ✓ Require a pull request before merging
    ✓ Require approvals (1)
    ✓ Dismiss stale pull request approvals when new commits are pushed
  ✓ Require status checks to pass
    Required:
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
      Analyze (go)
      Trivy filesystem vulnerability scan
      OpenSSF Scorecard analysis
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

## Packaging detection

Release packaging is owned by the Bazel harness. The release workflow calls
`make release`, which runs a Bazel target that provides GoReleaser, Go, syft,
and cosign from locked Bazel runfiles. There is no host GoReleaser install and
no separate CI-only release command.

Scorecard's Packaging heuristic may not recognise a Make/Bazel-wrapped
GoReleaser invocation as a literal `goreleaser/goreleaser-action` step. We prefer
the honest hermetic path over adding an install-only marker action that is never
used for the actual release.

The trigger filter in `release.yml` and the `tag_pattern` in `cliff.toml` are
anchored to stable SemVer tags (`vMAJOR.MINOR.PATCH`, no pre-release or
build-metadata suffix) so prerelease/metadata tags do not fire the publishing
pipeline.

[scorecard]: https://github.com/ossf/scorecard
[bp]: https://www.bestpractices.coreinfrastructure.org/
