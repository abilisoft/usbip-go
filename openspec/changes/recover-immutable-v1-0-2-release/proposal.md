## Why

The first `v1.0.2` release run failed before building any artifacts because the
release validator compared an annotated tag object's SHA with its peeled commit
SHA. The already-pushed, signed tag must remain immutable, while a protected,
auditable path is needed to publish that exact source revision without weakening
normal releases.

## What Changes

- Distinguish the pushed annotated tag-object identity from its peeled target
  commit throughout validation and checkout.
- Add regression coverage for annotated-tag push semantics.
- Add a one-version, fixed-input recovery workflow for the existing immutable
  `v1.0.2` tag. It revalidates the exact live tag object and target, runs all
  normal gates against the exact target commit, stages a draft, generates SLSA
  provenance, and publishes only after final revalidation.
- Record and document that recovery provenance identifies the protected
  default-branch recovery workflow and its fixed `release-tag=v1.0.2`
  confirmation input, while the repository-owned validator fixes and verifies
  the source commit rather than misrepresenting the run as a new tag-push event.
- Keep the normal Release workflow tag-push-only and keep every stable tag
  immutable; no tag is moved, deleted, or recreated.
- Non-goals: no general manual release launcher, no administrative tag-ruleset
  bypass, no relaxed signature/provenance checks, and no runtime or USB/IP
  protocol behavior change.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release-packaging`: Correct annotated-tag identity validation and define the
  fail-closed, fixed-source recovery publication contract.
- `developer-workflow`: Permit only the repository-defined one-version recovery
  workflow while retaining signed-tag-only normal releases and immutable tags.
- `security-release-quality`: Define verifiable recovery provenance and require
  the normal quality gates to run against the immutable release source.

## Impact

Affected areas are GitHub release automation, reusable workflow checkout inputs,
repository-owned release validators and tests, OpenSpec requirements and
traceability, and maintainer/security documentation. Public Go APIs, runtime
behavior, dependencies, and release artifact formats are unchanged.
