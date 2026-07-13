## ADDED Requirements

### Requirement: Git provenance checks declare their host dependency

Git provenance fixture tests SHALL resolve one exact Git executable, fail clearly when Git is unavailable, and declare the narrow host-tool execution requirement. The production release-stamping regression SHALL use committed constant status input instead of requiring Git inside its test action.

#### Scenario: Git provenance fixture tests run

- **WHEN** the version-helper or workspace-status fixture tests run
- **THEN** the Bazel targets are tagged `local` and `requires-git`
- **AND** each harness passes the resolved executable through `HARNESS_GIT`
- **AND** the release-stamping test remains sandboxable with constant workspace-status input
