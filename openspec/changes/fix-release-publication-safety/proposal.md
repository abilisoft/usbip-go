## Why

The first live `v1.0.1` run built a valid draft but failed provenance because
the SLSA generator was referenced by commit SHA and was not told to reuse a
draft release. The same run proved the documented GitHub Actions launcher
cannot create the signed annotated tag required by the repository ruleset, and
showed that Make/bootstrap diagnostics polluted generated release notes.

## What Changes

- Reference the SLSA generic generator by its verifier-required `@v2.1.0`
  identity and upload provenance into the existing draft release.
- Keep publication fail closed behind successful provenance and add a hermetic
  workflow-policy regression covering the complete draft/provenance contract.
- Reserve `make changelog` stdout for git-cliff output so diagnostics cannot
  pollute notes or falsely satisfy the nonempty-notes gate.
- **BREAKING (operations):** remove the unusable GitHub Actions manual release
  launcher and its API-created lightweight-tag helper. Stable releases start
  only from a locally signed annotated tag pushed from current `main`.
- Synchronize contributor/security guidance, accepted specifications, and
  exact traceability evidence.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release-packaging`: Make tag initiation, draft reuse, clean release notes,
  provenance identity, and fail-closed publication explicit.
- `developer-workflow`: Replace the broken dual-entry release contract with the
  signed tag-push Make/Bazel workflow.

## Impact

Affected surfaces are `.github/workflows/release.yml`, the Make changelog
entrypoint, release workflow policy tests, Bazel test suites, contributor and
security documentation, and OpenSpec traceability. No Go source, public v1 API,
wire protocol, runtime behavior, dependency, package format, or release asset
matrix changes. This change supersedes the incompatible release initiation
contract in the still-active `add-ui-release-trigger` change; that completed
change remains as historical implementation evidence until separately archived.
