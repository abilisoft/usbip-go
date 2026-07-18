## 1. Release Orchestration

- [x] 1.1 Use the verifier-compatible SLSA v2.1.0 identity, reuse the existing draft, and keep publication dependent on provenance
- [x] 1.2 Remove the incompatible GitHub Actions lightweight-tag launcher and retain the signed tag-push trigger
- [x] 1.3 Keep bootstrap and Make recipe diagnostics out of git-cliff release-note stdout

## 2. Regression and Documentation

- [x] 2.1 Add the hermetic release workflow policy test to the Bazel unit suite
- [x] 2.2 Synchronize accepted and delta OpenSpec behavior plus contributor/security guidance
- [x] 2.3 Reconcile exact traceability lines, counts, and regression evidence

## 3. Validation and Integration

- [x] 3.1 Run the focused policy test and relevant action, Make, shell, YAML, Bazel, and Markdown lint targets
- [x] 3.2 Run strict baseline/change OpenSpec validation and verify clean release-note output
- [x] 3.3 Create and verify a signed Conventional Commit, publish the branch through a pull request, and confirm all required checks
