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
| Pinned-Dependencies    | External actions use 40-character SHAs with trailing Dependabot version anchors. The SLSA reusable workflow uses its required release tag so verifier trust identity remains valid; local composite actions use repository paths. |
| SAST                   | `.github/workflows/codeql.yml` pins Go from `go.mod`, then traces the production binary through CodeQL's injected `go` wrapper via `make CODEQL_GO=go build-codeql`; the target isolates Go caches under `.local/codeql`, disables ambient Go configuration/workspaces/toolchain switching, and resolves dependencies from the committed vendor tree without network access. Analysis runs `security-and-quality` on every push/PR + weekly cron. |
| Filesystem CVEs        | `.github/workflows/trivy.yml` runs the required Trivy filesystem scan on pushes and pull requests plus the daily schedule; fork PRs scan without attempting a privileged SARIF upload. |
| Lint / config hygiene  | CI runs strict Bazel-backed formatter/lint drift checks for Go, Bazel, YAML, Markdown, shell, spelling, TOML, repository coverage, and GoReleaser config. `golangci_lint` uses the committed `vendor/` tree with network access blocked so its Go analysis remains sandboxed and remote-eligible. |
| Kernel integration     | `make test-integration` is a manual maintainer check on a capable Linux host with the required USB/IP/gadget modules and writable configfs. Standard GitHub-hosted runners cannot supply that privileged kernel surface, so automated CI runs the unprivileged unit, conformance, architecture, security, coverage, mutation, and release gates instead. |
| Vulnerabilities        | `make govulncheck` in PR, nightly, release, and local `make ci-local` gates; SARIF upload feeds code scanning.        |
| Token-Permissions      | Every workflow declares minimal top-level `permissions:`; jobs widen only for release or security uploads.   |
| Security-Policy        | [`SECURITY.md`](../SECURITY.md) at repo root.                                                                    |
| Signed-Releases        | GoReleaser + cosign keyless via GitHub OIDC; SBOM via syft. See `.goreleaser.yml`.                               |
| Dependency-Update-Tool | Dependabot weekly bumps for `gomod` + `github-actions`. See `.github/dependabot.yml`.                            |
| Fuzzing                | `internal/adapter/wire/fuzz_test.go` — codec fuzz targets seeded with historical malformed inputs.               |
| Maintained             | The development branch receives active maintenance; published support status is declared in [`SECURITY.md`](../SECURITY.md). |
| License                | Apache-2.0; SPDX header on every source file.                                                                    |
| Binary-Artifacts       | No executable release artifacts are tracked. Captured wire bytes are reviewed as inline hexadecimal test constants; temporary capture binaries are ignored. |
| Dangerous-Workflow     | No `pull_request_target` with checkout; no untrusted-input substitution into `run:` blocks.                      |
| Scorecard analysis     | `.github/workflows/scorecard.yml` runs `ossf/scorecard-action` on pushes and pull requests, branch-protection changes, the weekly schedule, and manual dispatch. Pull requests do not publish public results. |

## What the project owner enables on github.com

These two Scorecard checks require server-side configuration. The
repo content cannot enable them on its own.

1. **Active `default-branches` repository ruleset** on `main`:
   - Require two approvals, code-owner review, approval of the last push,
     stale-review dismissal, and resolution of review threads.
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
   - Require branches to be up to date before merging, signed commits,
     squash-only linear history, code-quality errors to be resolved, and the
     configured CodeQL/Scorecard/Trivy/govulncheck code-scanning thresholds.
   - Block branch creation, deletion, non-fast-forward updates, and ordinary
     direct updates. Organization administrators are the configured emergency
     bypass actors.

2. **Code-Review** policy is enforced by the pull-request rules above; the
   Scorecard result still depends on merged pull requests actually carrying
   independent approving reviews.

Current GitHub settings summary:

```text
Settings → Rules → Rulesets → Add branch ruleset for the default branch:
  ✓ Require a pull request before merging
    ✓ Require approvals (2)
    ✓ Require code-owner review
    ✓ Require approval of the last push
    ✓ Dismiss stale pull request approvals when new commits are pushed
    ✓ Require review-thread resolution
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
  ✓ Allow squash merges only
  ✓ Require configured CodeQL, Scorecard, Trivy, and govulncheck scanning
  ✓ Require code-quality errors to be resolved
  ✓ Restrict branch creation, deletion, and direct updates
    Organization administrators: emergency bypass
  ✓ Block force pushes
```

## OpenSSF Best Practices badge

The repo holds the **Passing** tier badge. The **Silver** and **Gold** tiers add
criteria such as multiple active committers, a documented governance model,
and independent security review. Higher tiers require both project work and
additional independent participation; this document does not promise a date.

## Where the score caps

Without help from a second maintainer, two Scorecard checks are
inherently capped:

- **Contributors** gives full credit only when the last 30 commits include at
  least five commits from contributors at each of three distinct companies. A
  solo-author repo scores 0 here regardless of code quality.
- **Code-Review** rewards merged PRs with approving reviews. A
  solo author can only satisfy this by working on branches and
  self-merging via a PR, which still records no independent approval.

Additional independent maintainers and reviewed pull requests improve these
checks, but full Contributors credit still depends on the number and
organizational diversity of contributors. These are participation constraints,
not code-quality defects.

## Packaging detection

Release packaging is owned by the Bazel harness. The release workflow calls
`make release`, which runs a Bazel target that provides GoReleaser, Go, syft,
and cosign from locked Bazel runfiles. There is no host GoReleaser install and
no separate CI-only release command.

Scorecard's Packaging heuristic may not recognise a Make/Bazel-wrapped
GoReleaser invocation as a literal `goreleaser/goreleaser-action` step. We prefer
the honest hermetic path over adding an install-only marker action that is never
used for the actual release.

The direct trigger filter in `release.yml`, its manual release-start form, and
the `tag_pattern` in `cliff.toml` are anchored to stable SemVer tags
(`vMAJOR.MINOR.PATCH`, no pre-release or build-metadata suffix). The manual path
creates the tag from the current default-branch head and redispatches at that
tag, so both entry points use the same gated tag-context publishing pipeline.

[scorecard]: https://github.com/ossf/scorecard
[bp]: https://www.bestpractices.coreinfrastructure.org/
