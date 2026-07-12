## 1. Manual Release Start

- [x] 1.1 Add a fail-closed release-start script for canonical tag validation, default-branch freshness, atomic tag creation, tag-context redispatch, and handoff rollback
- [x] 1.2 Add hermetic regression tests covering successful handoff and every validation/API/rollback failure path
- [x] 1.3 Register the script and tests in Bazel and the normal unit-test suite

## 2. Dual-Entry Workflow

- [x] 2.1 Add the GitHub Actions `workflow_dispatch` tag form while preserving the stable tag-push trigger
- [x] 2.2 Scope manual-start permissions to actions and contents writes, reject non-default branch starts, and converge both paths on identical tag-context release jobs
- [x] 2.3 Ensure checkout, concurrency, release publication, and SLSA provenance continue to use the actual stable tag context

## 3. Specifications and Documentation

- [x] 3.1 Update contributor release instructions for both supported entry points and explicitly exclude GitHub's Publish release form
- [x] 3.2 Update the main release/developer OpenSpec requirements and precise traceability evidence
- [x] 3.3 Validate `add-ui-release-trigger` and all main OpenSpec specs in strict mode

## 4. Validation and Publication

- [x] 4.1 Run focused script tests, actionlint, shellcheck, formatting, and documentation/config lint
- [x] 4.2 Run fresh normal CI, coverage, release snapshot, cross-compilation, and mutation validation
- [x] 4.3 Create signed Conventional Commits, audit live history, push the branch, open a new pull request, and verify all hosted checks
