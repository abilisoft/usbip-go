## Why

The correctness audit found that several repository safeguards and operator-facing paths fail open or lose essential evidence: an empty coverage report passes as perfect coverage, Bazel release binaries lack build provenance, untrusted Device descriptors can execute terminal controls, and `watch` exits successfully when its event source never starts or dies. These defects undermine release integrity and make automation report success when the requested behavior was not delivered.

## What Changes

- Reject coverage reports that contain no executable lines instead of treating `0/0` as 100 percent.
- Stamp Bazel-built distribution binaries with the release version, commit, and deterministic source-commit date, and recognize the repository's canonical `vMAJOR.MINOR.PATCH` tags in package-version derivation.
- Sanitize untrusted Device descriptor text at the human-terminal rendering boundary while preserving printable Unicode and leaving JSON escaping unchanged.
- Add an error-aware Importer event iterator and use it from `usbip-go watch` so subscription failures and unexpected source loss produce a non-zero command result; preserve the existing v1 `Watch` API as a compatibility wrapper.
- Add focused regression and contract tests for every failure mode and synchronize release, security, CLI, observability, public-API, and traceability documentation.

Non-goals: this change does not alter USB/IP Wire bytes, add terminal styling policy for trusted static labels, remove the compatibility `Watch` method, or introduce a second build/release system outside Make and Bazel.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `security-release-quality`: Coverage evidence fails closed when no executable lines are present, and human terminal output neutralizes untrusted control sequences.
- `release-packaging`: Bazel distribution binaries carry the canonical release version and build provenance.
- `operations-observability`: Build provenance remains accurate in Bazel artifacts and event-source loss is observable as an error.
- `cli-interface`: Human Device tables are terminal-safe and `watch` fails when monitoring cannot be established or is lost unexpectedly.
- `public-library-api`: Importer exposes an additive error-aware event iterator while preserving the v1 event-only iterator.

## Impact

Affected surfaces include `tools/scripts/coverage_check.sh`, Bazel workspace-status and Go binary rules, version helpers, CLI table/watch rendering, Importer event iteration, API baselines, focused shell/Go tests, OpenSpec deltas, and `openspec/TRACEABILITY.md`. No new runtime dependency or public API removal is introduced.
