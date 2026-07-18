## 1. Correct Annotated-Tag Validation

- [x] 1.1 Separate event tag-object, live tag-object, peeled event commit, live target commit, and checked-out commit inputs in the release validators.
- [x] 1.2 Check out `github.sha` in normal release and publication jobs and pass the distinct event identities to every validation step.
- [x] 1.3 Add focused pure/live validator regressions with distinct tag-object and commit fixtures plus missing and mismatched identity failures.
- [x] 1.4 Update the release workflow structural regression for the corrected inputs and immutable peeled-commit checkouts.

## 2. Gate the Exact Recovery Source

- [x] 2.1 Add an optional explicit source-ref input to reusable security, unit, conformance, architecture, and coverage workflows without changing existing caller defaults.
- [x] 2.2 Pass the peeled event commit explicitly from normal release jobs and add structural regressions proving each reusable checkout consumes the requested source ref.

## 3. Add Fixed v1.0.2 Recovery Automation

- [x] 3.1 Add repository-owned live recovery validators for the fixed tag identity, controller/source checkouts, bound draft ID, exact asset roster, and remote subject digests.
- [x] 3.2 Add focused recovery-validator tests covering identity changes, API failures, public replay, draft-ID replacement, asset changes, and remote digest mismatch.
- [x] 3.3 Add a fixed-confirmation, protected-main-only `v1.0.2` recovery workflow with exact-source prereq gates, separate control/source staging, draft release, SLSA `upload-tag-name`, final revalidation, and exact-draft publication.
- [x] 3.4 Add a structural recovery-workflow regression that rejects mutable inputs/refs, credentials, missing gates, unbound draft publication, or remote subjects not tied to the attested hashes.

## 4. Synchronize Specifications and Documentation

- [x] 4.1 Merge the delta requirements into release-packaging, developer-workflow, and security-release-quality current specs.
- [x] 4.2 Update contributor and security guidance with corrected annotated-tag semantics, the one-version recovery boundary, and honest provenance verification.
- [x] 4.3 Update `openspec/TRACEABILITY.md` with exact implementation and regression evidence.

## 5. Validate and Publish

- [x] 5.1 Run focused script/workflow tests and strict OpenSpec validation after the final edits.
- [x] 5.2 Run fresh low-memory `make format`, `make ci-local`, and `make release-snapshot` sequentially without overlapping Bazel servers.
- [ ] 5.3 Create and verify a signed Conventional Commit, open the pull request, and wait for every required exact-head check including mutation.
- [ ] 5.4 Merge only after checks pass, then verify fresh final-main CI and security workflows.
- [ ] 5.5 Dispatch the fixed recovery once and verify all gates, exact assets, checksums, Sigstore bundle, SLSA provenance, version/commit stamping, and public release metadata.
- [ ] 5.6 Verify the Go module proxy and pkg.go.dev observe the public `v1.0.2` module after publication.
