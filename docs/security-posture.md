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
| Kernel integration     | `make test-integration` is the privileged direct-host module/configfs suite. `make test-integration-vm` is a fail-closed two-guest KVM suite using a SHA-512-pinned Debian image, distinct guest kernels, guest-local privileged setup, and a direct QEMU stream link. Success requires both guests alive plus successful nonempty kernel, journal, system, and role evidence before scanning; cleanup preserves overlays unless every guest is confirmed stopped. Both are manual maintainer checks because standard GitHub-hosted runners supply neither required kernel surface. |
| Vulnerabilities        | `make govulncheck` in PR, nightly, release, and local `make ci-local` gates; SARIF upload feeds code scanning.        |
| Token-Permissions      | Every workflow declares minimal top-level `permissions:`; jobs widen only for release or security uploads.   |
| Security-Policy        | [`SECURITY.md`](../SECURITY.md) at repo root.                                                                    |
| Signed-Releases        | GoReleaser stages and reuses one draft; cosign signs keylessly through GitHub OIDC; syft emits SBOMs; and the verifier-compatible SLSA `@v2.1.0` workflow uploads provenance into that draft. Only the provenance-dependent publish job makes it public. The fixed `v1.0.2` recovery records protected-main workflow-dispatch provenance rather than claiming a second tag-push event. |
| Immutable-Tags         | Stable versions are single-use. The all-tag ruleset is authoritative; current release validation accepts only a fresh, signed, correctly targeted canonical tag event. Retract a consumed bad version and use the next patch rather than moving or recreating it. The sole `v1.0.2` recovery preserves its original object and source after a validator-only failure before artifact work. |
| Dependency-Update-Tool | Dependabot weekly bumps for `gomod` + `github-actions`. See `.github/dependabot.yml`.                            |
| Fuzzing                | `internal/adapter/wire/fuzz_test.go` — codec fuzz targets seeded with historical malformed inputs.               |
| Maintained             | The development branch receives active maintenance; published support status is declared in [`SECURITY.md`](../SECURITY.md). |
| License                | Apache-2.0; SPDX header on every source file.                                                                    |
| Binary-Artifacts       | No executable release artifacts are tracked. Captured wire bytes are reviewed as inline hexadecimal test constants; temporary capture binaries are ignored. |
| Dangerous-Workflow     | No `pull_request_target` with checkout; no untrusted-input substitution into `run:` blocks.                      |
| Scorecard analysis     | `.github/workflows/scorecard.yml` runs `ossf/scorecard-action` on pushes and pull requests, branch-protection changes, the weekly schedule, and manual dispatch. Pull requests do not publish public results. |

## What the project owner enables on github.com

These controls require server-side configuration. The repository content
cannot enable them on its own.

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

2. **Active `default-tags` repository ruleset** on all tags, including stable
   release tags:
   - Require signatures and restrict tag creation, updates, deletion, and
     non-fast-forward changes.
   - Organization administrators are emergency bypass actors, but routine
     release and recovery operations never use that bypass to move, delete, or
     recreate an existing stable tag.
   - Treat the first push as consuming the version even if artifact or
     provenance publication later fails; normally retract it and use the next
     patch. The repository-fixed `v1.0.2` exception never mutates its tag and
     applies only because the first job failed before any gate or artifact work.
   - Verify the Release workflow is enabled and active before creating a new
     stable tag; a disabled workflow still consumes a pushed version.

3. **Code-Review** policy is enforced by the pull-request rules above; the
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

Settings → Rules → Rulesets → Add tag ruleset for all tags:
  ✓ Require signatures
  ✓ Restrict tag creation, updates, and deletion
  ✓ Block non-fast-forward updates
    Organization administrators: emergency bypass
    Never use the bypass to move, delete, or recreate an existing stable tag
```

## Release integrity verification

Normal tag-push provenance is verified with `--source-tag vX.Y.Z`. The recovered
`v1.0.2` statement is intentionally different: `upload-tag-name` selected the
existing draft but did not turn its protected-main dispatch into tag-push
provenance. Download all `v1.0.2` assets into one directory and verify its nine
binary deliverables with:

```bash
artifacts=(
  usbip-go_1.0.2_linux_amd64.tar.gz
  usbip-go_1.0.2_linux_arm64.tar.gz
  usbip-go_1.0.2_linux_armv7.tar.gz
  usbip-go_1.0.2_linux_amd64.deb
  usbip-go_1.0.2_linux_arm64.deb
  usbip-go_1.0.2_linux_armv7.deb
  usbip-go_1.0.2_linux_amd64.rpm
  usbip-go_1.0.2_linux_arm64.rpm
  usbip-go_1.0.2_linux_armv7.rpm
)

slsa-verifier verify-artifact "${artifacts[@]}" \
  --provenance-path multiple.intoto.jsonl \
  --source-uri github.com/abilisoft/usbip-go \
  --source-branch main \
  --builder-id \
  'https://github.com/slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@refs/tags/v2.1.0' \
  --build-workflow-input release-tag=v1.0.2
```

Do not substitute `--source-tag v1.0.2`; that would falsely request tag-push
provenance. Verify the signed recovery configuration against the controller
commit disclosed in the GitHub Release body:

```bash
RECOVERY_CONTROLLER_SHA='<controller commit from the v1.0.2 release body>'
RECOVERY_ARTIFACT=usbip-go_1.0.2_linux_amd64.tar.gz
slsa-verifier verify-artifact "${RECOVERY_ARTIFACT}" \
  --provenance-path multiple.intoto.jsonl \
  --source-uri github.com/abilisoft/usbip-go \
  --source-branch main \
  --builder-id \
  'https://github.com/slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@refs/tags/v2.1.0' \
  --build-workflow-input release-tag=v1.0.2 \
  --print-provenance > verified-provenance.json

jq -e --arg sha "${RECOVERY_CONTROLLER_SHA}" '
  .predicate.invocation.configSource.uri ==
    "git+https://github.com/abilisoft/usbip-go@refs/heads/main" and
  .predicate.invocation.configSource.entryPoint ==
    ".github/workflows/recover-v1.0.2.yml" and
  .predicate.invocation.configSource.digest.sha1 == $sha and
  .predicate.invocation.environment.github_event_name == "workflow_dispatch" and
  .predicate.invocation.environment.github_event_payload.inputs["release-tag"] ==
    "v1.0.2"
' verified-provenance.json
```

Then verify every downloaded checksum entry, the keyless checksum signature,
and the source stamp in the amd64 archive:

```sh
sha256sum --check usbip-go_1.0.2_checksums.txt
cosign verify-blob \
  --bundle usbip-go_1.0.2_checksums.txt.sigstore.json \
  --certificate-identity \
  'https://github.com/abilisoft/usbip-go/.github/workflows/recover-v1.0.2.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  usbip-go_1.0.2_checksums.txt

tar xzf usbip-go_1.0.2_linux_amd64.tar.gz
version_output=$(NO_COLOR=1 ./usbip-go version)
printf '%s\n' "${version_output}" | grep -F 'usbip-go version 1.0.2'
printf '%s\n' "${version_output}" | \
  grep -F 'commit 72aa5a6b585d1f5b6230c8362254ea2a6296ec75'
printf '%s\n' "${version_output}" | \
  grep -F 'built 2026-07-18T15:02:10Z'
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

The explicit validation in `release.yml` and the `tag_pattern` in `cliff.toml`
are anchored to stable SemVer tags (`vMAJOR.MINOR.PATCH`, no pre-release,
build-metadata suffix, or leading-zero numeric component). Releases start only
from a newly created, GitHub-verified signed annotated tag pushed from the
current default-branch head. When the current workflow revision handles the
event, it rejects updated, forced, deleted, lightweight, unverified, stale, or
changed tag objects or targets before downstream work and exposes no manual
launcher. `github.event.after` is the annotated tag-object SHA, while
`github.sha` is its peeled commit. Later release and publication jobs check out
that immutable peeled commit without persisted Git credentials and revalidate
both live identities before draft staging and publication. Release notes use
git-cliff's current-tag selection and must begin with the pushed stable
version's heading.

GitHub selects a push workflow from the event's associated ref and revision.
Consequently, the workflow check is defense in depth, not protection against an
administrator moving a tag to an obsolete workflow revision or deleting and
recreating a consumed version. The active all-tag ruleset is the authoritative
boundary, and routine release or recovery never bypasses it. A proxy lookup is
post-release evidence, not a safe reuse check: a different proxy or checksum
database may already have cached the version.

The separately named `Recover v1.0.2` workflow is a fixed historical repair,
not a second normal release interface. It accepts only confirmation
`release-tag=v1.0.2`, binds the exact signed object
`f0c7083fdee40e1e31ebc170992fa5f43efe8d60` to commit
`72aa5a6b585d1f5b6230c8362254ea2a6296ec75`, runs all hosted gates against that
commit, binds the staged draft by release ID, and compares the GitHub SHA-256
digests of all 14 GoReleaser assets with their staged files and all nine
downloadable archive/package subjects with the hashes given to the SLSA
generator immediately before publishing that same ID. It becomes inert after
the exact public release exists.

[scorecard]: https://github.com/ossf/scorecard
[bp]: https://www.bestpractices.coreinfrastructure.org/
