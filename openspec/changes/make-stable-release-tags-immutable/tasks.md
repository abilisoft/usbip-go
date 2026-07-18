## 1. Module Recovery

- [x] 1.1 Retract `v1.0.1` in `go.mod` with the proxy-cache/tag-move reason and direct users to `v1.0.2` or later

## 2. Fail-Closed Release Validation

- [x] 2.1 Add a repository-owned validator for canonical fresh tag-creation event, signature, object-shape, and target metadata
- [x] 2.2 Invoke the live validator from initial validation and immediately before draft staging and publication without adding a launcher or changing required job names
- [x] 2.3 Add hermetic focused coverage for event, SemVer, annotated-tag signature, and target-consistency failures
- [x] 2.4 Pin later checkouts to the immutable event commit without persisted credentials and extend release workflow policy coverage for fail-closed revalidation
- [x] 2.5 Align release-note and build-stamping version parsing with canonical no-leading-zero SemVer
- [x] 2.6 Bind git-cliff to the stable tag at `HEAD` and reject a release-notes heading for any other version

## 3. Specification and Documentation

- [x] 3.1 Synchronize accepted release-packaging and developer-workflow requirements with the delta specifications
- [x] 3.2 Document immutable single-use tags, retraction recovery, and the prohibition on tag-ruleset bypass in contributor and security-posture guidance
- [x] 3.3 Update `openspec/TRACEABILITY.md` with exact repository evidence for the changed requirements and scenarios

## 4. Validation and Integration

- [x] 4.1 Run the focused hermetic validator and release workflow policy regressions
- [x] 4.2 Run lightweight shell, YAML, Markdown, formatting, and strict change/all-spec OpenSpec checks
- [x] 4.3 Run the repository Go/CI/release-snapshot gates and fresh mutation testing without weakening resource limits
- [x] 4.4 Create and verify a signed Conventional Commit with no attribution trailers, publish through a pull request, and confirm all required checks
- [ ] 4.5 Immediately before the single-use `v1.0.2` push, enable Release workflow ID `266335789` and independently verify its state is `active`
