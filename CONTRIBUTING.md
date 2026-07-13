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

Coverage thresholds use the aggregate executable-line denominator remaining
after configured exclusions. A zero aggregate fails as missing evidence. An
`LF:0` package alongside measured packages is reported as not coverable and is
not assigned a percentage or evaluated against the per-package threshold; the
measured aggregate and packages still determine the gate result.

Ordinary compile and unit-test actions declare their Bazel toolchains, inputs,
and runfiles so they remain eligible for remote caching and execution. Targets
such as `golangci_lint` use `exclusive-if-local` when they only need serialized
local execution, so remote workers may still execute them. Targets
tagged `local`, `requires-network`, `integration`, or `manual` are explicit
exceptions: repository-coverage checks inspect the live Git checkout, network
and conformance checks need external or loopback networking, kernel integration
needs host USB/IP facilities, and CodeQL must trace its direct Go wrapper build.
`bazel run` entrypoints also execute their final command on the Bazel client.

The committed `vendor/` tree exists only so `golangci_lint` can analyze the Go
module with network access blocked; Bazel/rules_go remains the production build
dependency resolver. After changing `go.mod` or `go.sum`, run
`make update-go-vendor` and commit the synchronized vendor tree. The module
hygiene gate checks that the vendored module graph loads without network access
and that regenerating it produces no byte-level diff.

Git-derived version-helper tests are the narrow local-tool exception. Their
Bazel targets are tagged `local` and `requires-git`, resolve one explicit host
Git executable before use, and fail clearly when it is unavailable. Bazel
workspace status itself also runs before the action graph and therefore uses
checkout Git. The production stamping regression does not share that exception:
`make check-release-stamping` supplies a committed constant workspace-status
fixture and executes the declared production binary in a sandboxable test.

Integration tests interact with kernel USB/IP surfaces and require root, a
writable configfs gadget tree, and loaded `dummy_hcd`, `libcomposite`,
`usb_f_acm`, `usb_f_mass_storage`, `usbip_core`, `usbip_host`, `usbip_vudc`, and
`vhci_hcd` modules for the complete suite. The kernel must enable
`CONFIG_USB_DUMMY_HCD`; otherwise the full bind/list/attach CLI scenario skips.
The tests are exposed as `make test-integration` and are not part of the default
unit-test or GitHub Actions targets. Run them manually on a capable Linux host
when changing kernel-facing behavior. Standard GitHub-hosted runners do not
provide the required kernel modules or privileged configfs surface, so the
automated pipeline deliberately limits itself to unprivileged, Bazel-backed
validation rather than treating an unavailable kernel environment as a test
failure or skip.

## Release process

The release workflow supports two equivalent entry points:

1. In GitHub, open **Actions**, select **Release**, click **Run workflow**, keep
   the branch set to the default branch, enter `vX.Y.Z`, and run it. The first
   run validates and creates the tag, then hands off to a second run at that tag.
2. From an up-to-date local default branch, create and push a tag directly:

   ```sh
   git tag -s vX.Y.Z -m 'Release vX.Y.Z'
   git push origin vX.Y.Z
   ```

Do not use GitHub's **Draft a new release** / **Publish release** form to create
the tag. That form couples tag creation to a GitHub Release, but this pipeline
must build and attest artifacts before publishing the draft.

Both supported entry points run `.github/workflows/release.yml` at the stable
tag. The workflow validates the tag, runs the reusable security, unit,
conformance, architecture, and coverage gates, re-runs the local CI sequence,
generates release notes through the Bazel-provisioned changelog target, and
then publishes with `make release`. GoReleaser, Go, syft, and cosign are all
resolved through Bazel runfiles. Kernel integration is a separate manual
maintainer check because it requires a specially provisioned Linux host.

For a local distribution build, `make dist` uses Bazel's release stamping to
derive the package version from a canonical `vMAJOR.MINOR.PATCH` tag (or a
deterministic development version), stamp the full source commit, and use that
commit's committer date as the reproducible build date. Ordinary unstamped
Bazel builds intentionally retain the binary's `dev`/`none`/`unknown`
fallbacks. Run `make check-release-stamping` to exercise this production target
end to end with fixed version, commit, and date inputs.

## Pull requests

Before opening a PR, run the narrow target for your change plus the relevant
gate (`make test`, `make lint`, `make test-coverage`,
`make check-release-stamping`, `make release-check`, or `make ci-local` for the
full repository-owned pull-request gate). Keep changes small, self-explanatory,
and covered by tests. Do not hide failing checks by narrowing CI-only commands;
add a Make target when the workflow needs a new reusable step.
