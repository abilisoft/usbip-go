## Why

The release pipeline is correctly gated but can only be initiated by creating a
tag outside GitHub's web UI. The maintainer needs both workflows: a local tag
push for advanced use and a safe **Actions → Release → Run workflow** form that
creates the requested tag without prematurely publishing a GitHub Release.

## What Changes

- Add a manual `workflow_dispatch` entry point accepting a canonical stable tag.
- When dispatched from the default branch, validate the tag and current branch
  head, create the tag atomically, and explicitly redispatch the same release
  workflow at that tag.
- Preserve direct `vMAJOR.MINOR.PATCH` tag pushes as an equivalent release entry
  point.
- Keep all security, unit, conformance, architecture, coverage, build,
  provenance, draft, and publish gates identical after either entry point.
- Add hermetic regression tests and maintainer documentation for both paths and
  their failure/rollback behavior.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `release-packaging`: stable releases can be initiated by either a direct tag
  push or a GitHub Actions manual form, with both converging on the same
  tag-context release pipeline.
- `developer-workflow`: the documented maintainer release process exposes both
  supported entry points without using GitHub's Publish release form.

## Impact

- Affects `.github/workflows/release.yml`, a repository-owned release-start
  script and tests, Make/Bazel test registration, contributor documentation,
  OpenSpec, and traceability.
- Requires only the workflow-scoped `actions: write` and `contents: write`
  permissions for the manual tag-start job; normal release jobs retain their
  existing least-privilege permissions.
- Adds no runtime dependency and changes no Go, CLI, wire, or public API
  behavior.
