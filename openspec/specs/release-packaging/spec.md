## Purpose

Specify the release workflow, GoReleaser packaging contract, artifact integrity, and provenance behavior for published usbip-go releases.

## Requirements

### Requirement: Release workflow only publishes canonical stable SemVer tags
The GitHub release workflow SHALL publish only tags matching `vMAJOR.MINOR.PATCH`.

#### Scenario: Stable tag is pushed
- **WHEN** a tag such as `v1.2.3` is pushed
- **THEN** the release workflow is eligible to continue after the tag validation job

#### Scenario: Prerelease tag is pushed
- **WHEN** a tag such as `v1.2.3-rc1` is pushed
- **THEN** the workflow trigger excludes it from release publication

#### Scenario: Non-canonical tag reaches validation
- **WHEN** a tag such as `v1.2.3foo` or `v1.2.3+build.7` reaches the workflow
- **THEN** the validate-tag job rejects it before artifacts are built

### Requirement: Release publication waits for prereq gates
The release job SHALL depend on reusable security, unit-test, conformance, architecture, and coverage workflows.

#### Scenario: Prereq gate fails
- **WHEN** any prereq workflow fails
- **THEN** the build-and-publish release job does not run

#### Scenario: Prereq gates pass
- **WHEN** security, unit tests, conformance, architecture checks, and coverage complete successfully
- **THEN** the release job may build and publish artifacts

### Requirement: Release notes come from git-cliff
The release workflow SHALL generate release notes for the pushed tag with git-cliff and fail before artifact publication if the rendered notes are empty.

#### Scenario: Release notes render
- **WHEN** the release job checks out the tag with full history
- **THEN** `git-cliff --latest --strip header` writes `build/release-notes.md`
- **AND** that file is passed to GoReleaser through `--release-notes`

#### Scenario: Release notes are empty
- **WHEN** `build/release-notes.md` is zero bytes
- **THEN** the workflow emits an error and refuses to release

### Requirement: GoReleaser builds a single pure-Go Linux binary matrix
GoReleaser SHALL build the `./cmd/usbip-go` single binary for Linux `amd64`, `arm64`, and `armv7` with cgo disabled.

#### Scenario: Release binary is built
- **WHEN** GoReleaser runs the `usbip-go` build
- **THEN** `CGO_ENABLED=0` is set
- **AND** `-trimpath`, `-s`, and `-w` are used
- **AND** `main.version`, `main.commit`, and `main.buildDate` are stamped from tag/version, commit, and commit-date metadata

#### Scenario: Snapshot release runs locally
- **WHEN** `task release:snapshot` dispatches to `ci:release:snapshot`
- **THEN** GoReleaser runs with `--snapshot --clean --skip=sign,sbom,publish`

### Requirement: Release archives include operator documentation and units
GoReleaser SHALL package tar.gz archives with the binary plus core repository documentation and systemd unit files.

#### Scenario: Archive is produced
- **WHEN** a release archive is generated
- **THEN** its name includes project, version, OS, architecture, and ARM variant when applicable
- **AND** it includes `LICENSE`, `README.md`, `CONTRIBUTING.md`, `contrib/systemd/usbip-go.service`, `contrib/systemd/usbip-go.socket`, and `docs/*.md`

### Requirement: OS packages install binary, docs, and systemd units
GoReleaser nfpm packaging SHALL produce Debian and RPM packages for the same build IDs.

#### Scenario: Package is produced
- **WHEN** nfpm emits a package
- **THEN** the binary installs under `/usr/bin`
- **AND** the systemd service and socket install under `/usr/lib/systemd/system`
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
The release workflow SHALL produce SLSA provenance for downloadable binary archives and OS packages.

#### Scenario: Artifact hashes are collected
- **WHEN** GoReleaser has produced release output under `build/dist`
- **THEN** the release job hashes `*.tar.gz`, `*.deb`, and `*.rpm` artifacts
- **AND** exposes the base64-encoded sha256 list to the provenance job

#### Scenario: Provenance is generated
- **WHEN** the provenance job runs
- **THEN** the SLSA generic generator uses the release job's artifact hashes
- **AND** uploads provenance assets to the GitHub Release
