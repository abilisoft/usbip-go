## ADDED Requirements

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
