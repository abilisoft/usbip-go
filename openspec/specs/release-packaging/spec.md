## Purpose

Specify the release workflow, GoReleaser packaging contract, artifact integrity, and provenance behavior for published usbip-go releases.

## Requirements

### Requirement: Release workflow only publishes canonical stable SemVer tags

The GitHub release workflow SHALL publish only tags matching
`vMAJOR.MINOR.PATCH`. The supported maintainer entry point SHALL be a direct
push of a signed annotated tag created once from the current default-branch
head. The validation job SHALL continue only when the push event identifies a
fresh tag creation with `created` equal to `true`, `forced` equal to `false`,
and `deleted` equal to `false`; the live ref is a GitHub-verified signed
annotated tag pointing directly to a commit; its name and target match the
event; and its target equals the checked-out default-branch head. The workflow
SHALL expose only a direct tag-push entry point, SHALL continue to publication
only after validating the fresh canonical stable tag, and SHALL NOT expose a
manual GitHub Actions launcher that cannot produce the required tag signature.
After initial validation, release and publication jobs SHALL check out the
immutable event commit without persisted Git credentials and SHALL revalidate
the live signed annotated tag against that commit immediately before staging or
publishing the draft release.
The active server-side tag ruleset SHALL remain the authoritative immutability
boundary because GitHub selects push workflow code from the event ref and
revision before the current workflow can validate it.

#### Scenario: Fresh stable tag is pushed

- **WHEN** a newly created signed tag such as `v1.2.3` is pushed directly
- **AND** the event reports `created=true`, `forced=false`, and `deleted=false`
- **AND** the current workflow revision verifies the annotated tag signature, name, commit target, event target, and default-branch head are identical where required
- **THEN** the release workflow is eligible to continue after the tag validation job

#### Scenario: Release source is consumed after validation

- **WHEN** the release or publication job checks out repository-owned release code after tag validation
- **THEN** it checks out the immutable event target rather than dereferencing the live tag
- **AND** the checkout does not persist GitHub credentials

#### Scenario: Live release tag changes after initial validation

- **WHEN** the live tag object, signature state, or commit target no longer matches the validated event immediately before draft staging or publication
- **THEN** the current release job fails before performing the protected operation
- **AND** it does not continue on the revalidation error

#### Scenario: Fresh stable tag targets the wrong commit

- **WHEN** a fresh canonical tag event reaches the current workflow but its commit target differs from the event target or checked-out default-branch head
- **THEN** the validate-tag job fails visibly before artifacts are built
- **AND** the version remains consumed and any later attempt uses a higher patch

#### Scenario: Release tag is lightweight or unverified

- **WHEN** a fresh canonical tag event reaches the current workflow but the live ref is not a GitHub-verified signed annotated tag pointing directly to a commit
- **THEN** the validate-tag job fails visibly before artifacts are built

#### Scenario: Existing stable tag move reaches the current workflow

- **WHEN** a stable tag push reports `created` other than `true` or `forced=true`
- **THEN** the validate-tag job fails visibly before artifacts are built
- **AND** no downstream release, provenance, or publication job runs

#### Scenario: Existing stable tag targets obsolete workflow code

- **WHEN** an actor attempts to move an existing stable tag to a revision whose release workflow predates current validation
- **THEN** the active server-side tag ruleset rejects the ref mutation
- **AND** maintainers do not use administrative bypass because the event-ref workflow cannot provide the current validation guarantee

#### Scenario: Stable tag deletion reaches validation

- **WHEN** a stable tag event reports `deleted` other than `false`
- **THEN** the validate-tag job fails visibly before artifacts are built
- **AND** no downstream release, provenance, or publication job runs

#### Scenario: Prerelease tag is pushed

- **WHEN** a tag such as `v1.2.3-rc1` is pushed
- **THEN** the workflow trigger excludes it from release publication

#### Scenario: Non-canonical tag reaches validation

- **WHEN** a tag such as `v1.2.3foo`, `v1.2.3+build.7`, or `v01.2.3` reaches validation
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

The release workflow SHALL generate release notes for the exact stable release
tag at `HEAD` with git-cliff `--current` and redirect only the renderer's stdout
into the release-notes file. Bootstrap diagnostics and Make recipe echo SHALL
remain outside that file. The workflow SHALL fail before artifact publication
if the renderer's output is empty or its first heading does not identify the
pushed stable tag.

#### Scenario: Release notes render

- **WHEN** the release job checks out the immutable event target with full history and its stable tag is at `HEAD`
- **THEN** only `git-cliff --current --strip header` stdout writes `build/release-notes.md`
- **AND** setup and build diagnostics are excluded from the rendered release notes
- **AND** the first heading identifies the pushed stable tag
- **AND** that file is passed to GoReleaser through `--release-notes`

#### Scenario: Release notes are empty

- **WHEN** git-cliff renders zero bytes to stdout
- **THEN** `build/release-notes.md` remains zero bytes
- **AND** setup and build diagnostics do not make the file nonempty
- **AND** the workflow emits an error and refuses to release

#### Scenario: Release notes identify a different version

- **WHEN** the rendered release-notes heading does not identify the pushed stable tag
- **THEN** the workflow emits an error and refuses to stage release artifacts

### Requirement: GoReleaser builds a single pure-Go Linux binary matrix

GoReleaser SHALL build the `./cmd/usbip-go` single binary for Linux `amd64`, `arm64`, and `armv7` with cgo disabled.

#### Scenario: Release binary is built

- **WHEN** GoReleaser runs the `usbip-go` build
- **THEN** `CGO_ENABLED=0` is set
- **AND** `-trimpath`, `-s`, and `-w` are used
- **AND** `main.version`, `main.commit`, and `main.buildDate` are stamped from tag/version, commit, and commit-date metadata

#### Scenario: Snapshot release runs locally

- **WHEN** `make release-snapshot` dispatches to the Bazel `//:release-snapshot` target
- **THEN** GoReleaser runs with `--snapshot --clean` through the Bazel-provisioned release harness

### Requirement: Bazel distribution binaries carry build provenance

The Make/Bazel distribution path SHALL stamp the production `usbip-go` binary with the canonical version, commit, and build date derived from declared workspace-status inputs. Canonical `vMAJOR.MINOR.PATCH` Git tags SHALL normalize to package version `MAJOR.MINOR.PATCH` where packaging metadata requires an unprefixed version.

#### Scenario: Tagged distribution binary is built

- **WHEN** `make dist` builds from a canonical stable tag
- **THEN** `usbip-go version` reports that tag's release version
- **AND** it reports the source commit and deterministic build-date metadata instead of compiled fallback values

#### Scenario: Development distribution binary is built

- **WHEN** `make dist` builds from a commit without an exact stable tag
- **THEN** the version is a deterministic development version derived from repository state
- **AND** commit and build-date metadata remain populated

#### Scenario: Release provenance regression runs

- **WHEN** `make check-release-stamping` runs
- **THEN** Bazel builds the production `usbip-go` target under the release configuration with committed deterministic workspace-status values
- **AND** the regression executes that declared production artifact and verifies its exact version, commit, and build date

### Requirement: Release archives include operator documentation and deployment files

GoReleaser SHALL package tar.gz archives with the binary plus core repository documentation, systemd unit files, and the modules-load snippet.

#### Scenario: Archive is produced

- **WHEN** a release archive is generated
- **THEN** its name includes project, version, OS, architecture, and ARM variant when applicable
- **AND** it includes `LICENSE`, `README.md`, `CONTRIBUTING.md`, `contrib/systemd/usbip-go.service`, `contrib/systemd/usbip-go.socket`, `contrib/modules-load.d/usbip-go.conf`, and `docs/*.md`

### Requirement: OS packages install binary, docs, systemd units, and modules-load config

GoReleaser nfpm packaging SHALL produce Debian and RPM packages for the same build IDs.

#### Scenario: Package is produced

- **WHEN** nfpm emits a package
- **THEN** the binary installs under `/usr/bin`
- **AND** the systemd service and socket install under `/usr/lib/systemd/system`
- **AND** the modules-load snippet installs under `/usr/lib/modules-load.d`
- **AND** README and LICENSE install under `/usr/share/doc/usbip-go`

### Requirement: Checksums and SBOMs are generated

The release SHALL publish a sha256 checksums file and SPDX JSON SBOM documents for archives.

#### Scenario: Checksums are generated

- **WHEN** GoReleaser completes artifact generation
- **THEN** it writes `usbip-go_<version>_checksums.txt` using SHA-256

#### Scenario: SBOMs are generated

- **WHEN** SBOM generation is enabled in the release workflow
- **THEN** each archive receives a `${artifact}.sbom.json` SPDX document

### Requirement: Checksums are keylessly signed with Sigstore

The release workflow SHALL sign the checksums file with cosign keyless signing using GitHub OIDC.

#### Scenario: Signing runs

- **WHEN** GoReleaser reaches the sign step
- **THEN** cosign signs the checksum artifact
- **AND** writes a Sigstore bundle named `${artifact}.sigstore.json`

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
