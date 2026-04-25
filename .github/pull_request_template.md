<!--
Fill in every section. The reviewer will reject PRs that ship with
this template still containing placeholders.
-->

## Summary

<!-- One or two sentences on what changes and why. -->

## TDD trace

- RED commit: <!-- e.g. abc1234 -- test(app): <subject> -->
- GREEN commit: <!-- e.g. def5678 -- feat(app): <subject> -->

<!--
If the PR is multiple RED/GREEN pairs, list each pair. Refactor-only
commits are fine here — label them "refactor: <subject>".
The CI `test-tdd-discipline` job enforces the chain; mismatch fails
the build.
-->

## Contract trace

- Contract section(s): <!-- e.g. §5.5 auto-reconnect, §11.5.5 metrics -->
- Related work item: <!-- e.g. Prometheus metrics catalogue -->

## Gates passed

- [ ] `task lint` → `0 issues.`
- [ ] `task test` → race-clean
- [ ] `task vuln` → clean
- [ ] `task build` → produces `build/bin/usbip-go` + `build/bin/usbipd-go`
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
