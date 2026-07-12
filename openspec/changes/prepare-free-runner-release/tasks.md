## 1. Hosted Release Pipeline

- [x] 1.1 Remove the privileged kernel workflow and its stable-release dependency
- [x] 1.2 Preserve the normal security, unit, conformance, architecture, coverage, mutation, local-CI, packaging, and provenance gates
- [x] 1.3 Keep `make test-integration` documented as a separate manual check for a capable Linux host
- [x] 1.4 Correct Linux driver-core exporter event mapping with focused regressions

## 2. Release Documentation

- [x] 2.1 Replace temporary all-tags-retracted wording with version-agnostic supported-release policy and installation guidance
- [x] 2.2 Add a versionless pkg.go.dev badge/link and document the hosted-runner limitation accurately
- [x] 2.3 Update main OpenSpec requirements and `openspec/TRACEABILITY.md` with precise implementation evidence

## 3. Validation and Publication

- [x] 3.1 Run fresh workflow, Markdown/YAML, Go, and strict OpenSpec validation
- [x] 3.2 Run fresh repository CI, release-snapshot, cross-compilation, and mutation gates
- [x] 3.3 Record the existing manual local kernel-integration validation without claiming it ran in GitHub Actions
- [x] 3.4 Create signed Conventional Commits, audit live attribution/signature metadata, push the branch, and open a pull request
