<!--
Fill in every section. The reviewer will reject PRs that ship with
this template still containing placeholders.
-->

## Summary

<!-- One or two sentences on what changes and why. -->

## TDD trace

- RED commit: <!-- e.g. abc1234 -- feat(app): tests only, no impl yet -->
- GREEN commit: <!-- e.g. def5678 -- feat(app): impl that satisfies the tests -->

<!--
The CI `TDD commit discipline` job (in `ci.yml`, PR-only) treats a
feat:/fix: commit that adds new *_test.go AND touches no non-test
.go outside internal/tools/ as RED, and requires the very next
commit to touch non-test .go outside internal/tools/ (the GREEN;
additions OR modifications both count) or be a refactor: commit.
test:-prefixed commits are NOT carried forward as RED — they're
treated as coverage hardening for already-shipped code. If the
PR has multiple RED/GREEN pairs, list each pair here. See
CONTRIBUTING.md "TDD discipline" for the full gate semantics.
-->

## Contract trace

- Contract section(s): <!-- e.g. §5.5 auto-reconnect, §11.5.5 metrics -->
- Related work item: <!-- e.g. Prometheus metrics catalogue -->

## Gates passed

- [ ] `task lint` → `0 issues.`
- [ ] `task test` → race-clean
- [ ] `task vuln` → clean
- [ ] `task build` → produces `build/bin/usbip-go`
- [ ] `go build ./examples/...` → clean
- [ ] `task test:cover` thresholds met (if `pkg/` or `internal/app` touched)
- [ ] `task test:integration` run locally (when the change touches
      kernel-adapter paths)
- [ ] `task test:conformance` run locally (when the change touches
      wire codec or handshake)

## Breaking-change check

- [ ] This PR does NOT change the public API surface.
- [ ] OR — this PR is `BREAKING:` and includes regenerated
      `api/pkg_usbip.json` / `api/pkg_domain.json` baselines:
      ```
      apidiff -w api/pkg_usbip.json  github.com/abilisoft/usbip-go/pkg/usbip
      apidiff -w api/pkg_domain.json github.com/abilisoft/usbip-go/pkg/domain
      ```

## Notes for reviewers

<!-- Anything tricky, deferred, or out-of-scope. If empty, delete this section. -->
