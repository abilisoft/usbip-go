## Why

`v1.0.1` was moved after a public Go module proxy had cached its original
source, so direct resolution and proxy/sumdb resolution no longer agreed on the
module checksum. Stable release tags must be single-use: once pushed, a failed
or incorrect version is retracted by a later version rather than repaired by
moving the tag.

## What Changes

- Retract `v1.0.1` with an explicit historical module-proxy/tag-rewrite reason
  and direct users to `v1.0.2` or later.
- Make current release validation fail visibly unless the push is a fresh tag
  creation whose GitHub-verified annotated tag targets the exact current
  default-branch head, before downstream release work can run.
- Pin later release and publication checkouts to the immutable event commit,
  discard checkout credentials, and revalidate the live tag before staging or
  publication.
- Bind git-cliff release notes to the stable tag at `HEAD` and reject a heading
  that identifies any other version.
- Add hermetic regression coverage for event identity, canonical SemVer,
  annotated-tag metadata, signature state, and target consistency.
- Document that stable versions and their tags are immutable, tag-protection
  bypasses are never used to move or recreate them, and a bad pushed version is
  retracted before the next patch version is published.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release-packaging`: Require a fresh, signed, correctly targeted stable tag
  creation and fail closed when current validation receives invalid metadata.
- `developer-workflow`: Require maintainers to retract a failed pushed version
  and advance SemVer instead of moving, deleting, recreating, or bypassing
  protection for its tag.

## Impact

Affected surfaces are `go.mod`, stable-version parsing and tag-bound rendering
for release notes and stamping, immutable Release workflow checkouts, repeated
read-only GitHub tag-object validation, repository-owned hermetic policy
regressions, contributor and security-posture documentation, accepted OpenSpec
requirements, and traceability. There is no Go runtime, public v1 API, wire
protocol, dependency graph, package format, artifact matrix, or release
launcher change.
