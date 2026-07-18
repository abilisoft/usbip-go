## MODIFIED Requirements

### Requirement: Release workflow only publishes canonical stable SemVer tags

The GitHub release workflow SHALL publish only tags matching
`vMAJOR.MINOR.PATCH`. The supported maintainer entry point SHALL be a direct
push of a signed annotated tag created from the current default-branch head.
The workflow SHALL expose only a direct tag-push entry point, SHALL continue to
publication only after validating a canonical stable tag, and SHALL NOT expose a
manual GitHub Actions launcher that cannot produce the signature required by the
repository tag ruleset.

#### Scenario: Stable tag is pushed

- **WHEN** a tag such as `v1.2.3` is pushed directly
- **THEN** the release workflow is eligible to continue after the tag validation job

#### Scenario: Prerelease tag is pushed

- **WHEN** a tag such as `v1.2.3-rc1` is pushed
- **THEN** the workflow trigger excludes it from release publication

#### Scenario: Non-canonical tag reaches validation

- **WHEN** a tag such as `v1.2.3foo` or `v1.2.3+build.7` reaches validation
- **THEN** the validate-tag job rejects it before artifacts are built

### Requirement: Release publication waits for prereq gates

The release job SHALL depend on reusable security, unit-test, conformance,
architecture, and coverage workflows that run on the standard GitHub-hosted
runner pool available to the project. Single-kernel module integration and
two-guest KVM resilience SHALL remain separate manual maintainer checks because
they require privileged Linux kernel or virtualization capabilities unavailable
on those runners.

#### Scenario: Prereq gate fails

- **WHEN** any prereq workflow fails for the tagged release
- **THEN** the draft-building release job does not run

#### Scenario: Prereq gates pass

- **WHEN** security, unit tests, conformance, architecture checks, and coverage complete successfully
- **THEN** the release job may build and stage draft artifacts for downstream attestation and publication

#### Scenario: Kernel integration requires privileged capabilities

- **WHEN** the project has only standard GitHub-hosted runners
- **THEN** the release workflow does not schedule kernel-module, writable-configfs, or KVM integration tests
- **AND** maintainers can run `make test-integration` on a capable Linux host and `make test-integration-vm` on a capable Linux KVM host

### Requirement: Release notes come from git-cliff

The release workflow SHALL generate release notes for the stable release tag
with git-cliff and redirect only the renderer's stdout into the release-notes
file. Bootstrap diagnostics and Make recipe echo SHALL remain outside that file.
The workflow SHALL fail before artifact publication if the renderer's output is
empty.

#### Scenario: Release notes render

- **WHEN** the release job checks out the tag with full history
- **THEN** only `git-cliff --latest --strip header` stdout writes `build/release-notes.md`
- **AND** setup and build diagnostics are excluded from the rendered release notes
- **AND** that file is passed to GoReleaser through `--release-notes`

#### Scenario: Release notes are empty

- **WHEN** git-cliff renders zero bytes to stdout
- **THEN** `build/release-notes.md` remains zero bytes
- **AND** setup and build diagnostics do not make the file nonempty
- **AND** the workflow emits an error and refuses to release

### Requirement: SLSA provenance covers user-downloadable binary artifacts

The release workflow SHALL produce SLSA provenance for downloadable binary
archives and OS packages. It SHALL invoke the generic generator with the exact
verifier-compatible reusable-workflow identity
`slsa-framework/slsa-github-generator/.github/workflows/generator_generic_slsa3.yml@v2.1.0`.
GoReleaser SHALL stage and reuse one draft GitHub Release. The provenance job
SHALL upload into that existing release with `draft-release: 'true'`, and only a
publish job that depends on successful provenance SHALL make the release public.

#### Scenario: Artifact hashes are collected

- **WHEN** GoReleaser has produced release output under `build/dist`
- **THEN** the release job hashes `*.tar.gz`, `*.deb`, and `*.rpm` artifacts
- **AND** exposes the base64-encoded sha256 list to the provenance job

#### Scenario: Provenance is generated

- **WHEN** the provenance job runs
- **THEN** the SLSA generic generator at `@v2.1.0` uses the release job's artifact hashes
- **AND** uploads provenance assets to the existing draft GitHub Release
- **AND** keeps that release unpublished until the dependent publish job runs

#### Scenario: Provenance generation fails

- **WHEN** the SLSA generator fails before uploading valid provenance
- **THEN** the draft GitHub Release remains unpublished
- **AND** the dependent publish job does not run
