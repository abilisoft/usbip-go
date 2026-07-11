# Contributing

usbip-go uses a Bazel-first build harness with a thin Make interface. CI calls
the same `make` targets that developers run locally; if a command happens in a
pipeline, prefer adding or reusing a Make target instead of spelling out a
separate CI-only command.

## Toolchain

The repository bootstraps its own tools under `.local/`:

- Bazelisk and the Go toolchain are installed by `make bootstrap`.
- Bazel/Bzlmod resolves Go dependencies, linters, formatters, GoReleaser,
  syft, cosign, and other release tools.
- Do not depend on host-installed Nix, Task, GoReleaser, golangci-lint, or Go
  binaries for normal development or CI.

## Common commands

```bash
make help              # list supported targets
make bootstrap         # install repo-local Go + Bazelisk
make format            # format Go, Bazel, shell, TOML, YAML, and Gazelle files
make build             # build all Bazel targets
make ci-local          # run the GitHub PR CI pipeline locally
make test              # unit tests only; excludes integration/conformance/lint/manual targets
make test-conformance  # wire conformance tests
make test-integration  # kernel/integration-tagged tests
make test-coverage     # Bazel coverage plus configured thresholds
make lint              # strict lint suite; do not disable checks to make this pass
make govulncheck       # Go vulnerability scan
make release-check     # validate .goreleaser.yml with Bazel-provisioned GoReleaser
make release-snapshot  # local snapshot packaging
make release           # tagged release publish path, used by GitHub Actions
make clean-all         # remove Bazel output and repo-local tool/cache state
```

## Lint and formatting policy

Keep linting strict. Do not weaken `.golangci.yml`, skip Bazel lint targets, add
blanket `nolint`, or remove checks to get a green run. Fix the underlying issue.
If a suppression is genuinely needed, scope it to the exact linter and include a
clear justification.

The `make lint` target is intentionally broad: Go lint, Gazelle drift,
Buildifier, Checkmake, actionlint, gitleaks, Markdown, shell, spelling, TOML,
YAML, and repository coverage checks all run through Bazel.

## Commit policy

Every commit must use a Conventional Commit subject and carry a verifiable
cryptographic signature. Use the repository's configured Git identity and
signing key; if signing fails, stop rather than creating an unsigned commit.
Never add a `Co-authored-by` trailer.

Pull-request CI checks each commit in the PR range for the conventional subject
shape, a signature header, GitHub's cryptographic verification result, and
forbidden co-author trailers. The default-branch ruleset independently requires
GitHub-verified signatures and squash-only linear history.

## TDD discipline

The PR-only `TDD commit discipline` job recognizes a `feat:` or `fix:` commit as
RED when it adds a `_test.go` file without touching production Go outside
`internal/tools/`. The immediately following commit must touch production Go or
be a `refactor:` commit. A PR may contain multiple RED/GREEN pairs but may not
end with a dangling RED commit. `test:` commits are coverage hardening and do
not open a RED/GREEN pair.

This same job verifies that every commit in the PR range is Conventional,
contains a signature header that GitHub verifies, and has no `Co-authored-by`
trailer. Incompatible public API changes additionally require a Conventional
Commit breaking marker (`!` in the subject or a `BREAKING CHANGE:` footer) and
regenerated API baselines.

## Tests

Default PR/push CI preserves the repository ruleset contexts by running
reusable Make/Bazel jobs for security/lint/vulnerability scanning, unit tests,
wire conformance, coverage thresholds, architecture/API compatibility, and TDD
commit discipline. `make ci-local` runs the repository-owned command sequence
locally through the Bazel-backed runner: build, unit and race tests,
conformance, lint, govulncheck, coverage thresholds, and GoReleaser config
validation. GitHub-only
services such as CodeQL, Trivy, Scorecard, Codecov upload, and SARIF upload stay
in Actions. The local runner is intentionally host-native rather than
containerized because Bazel already provisions the Go SDK, dependencies, and
lint/release tools hermetically. Add an opt-in container wrapper only if a
future CI-only OS dependency appears. Nightly reuses the security, unit,
conformance, and coverage jobs and adds a snapshot release packaging pass.

Integration tests interact with kernel USB/IP surfaces and may require a Linux
host with suitable kernel modules and privileges. They are exposed as
`make test-integration` and are not part of the default unit-test target.

## Release process

Pushing a stable tag `vMAJOR.MINOR.PATCH` runs `.github/workflows/release.yml`.
The workflow validates the tag, runs the reusable security, unit, conformance,
architecture, coverage, and dedicated kernel-integration gates, re-runs the
local CI sequence, generates release notes through the Bazel-provisioned
changelog target, and then publishes with `make release`. GoReleaser, Go, syft,
and cosign are all resolved through Bazel runfiles.

## Pull requests

Before opening a PR, run the narrow target for your change plus the relevant
gate (`make test`, `make lint`, `make test-coverage`, `make release-check`, or
`make ci-local` for the full repository-owned pull-request gate). Keep changes
small, self-explanatory, and covered by tests. Do not hide failing checks by
narrowing CI-only commands; add a Make target when the workflow needs a new
reusable step.
