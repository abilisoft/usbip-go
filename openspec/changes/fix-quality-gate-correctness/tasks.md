## 1. Coverage Evidence

- [x] 1.1 Reject LCOV input with zero included executable lines before percentage calculation
- [x] 1.2 Add and register hermetic regressions for empty, missing-`LF`, all-zero-`LF`, excluded-only, and valid reports
- [x] 1.3 Preserve mixed zero-line and measured reports without inventing a per-package percentage

## 2. Bazel Release Provenance

- [x] 2.1 Normalize canonical `vMAJOR.MINOR.PATCH` tags and add fixed-repository version-helper regressions
- [x] 2.2 Replace wall-clock workspace status with stable commit-derived build metadata and test exact deterministic output
- [x] 2.3 Wire version, commit, and build-date `x_defs` into the production Go binary
- [x] 2.4 Add a dedicated production-target release-config gate with fixed workspace-status inputs
- [x] 2.5 Make Git fixture tests resolve an exact executable and declare their narrow local requirement

## 3. Terminal-Safe Human Rendering

- [x] 3.1 Escape C0, DEL, and C1 controls in every dynamic table cell before styling
- [x] 3.2 Add regressions for C0, OSC, CSI, C1, printable Unicode, and unchanged JSON escaping

## 4. Error-Observable Event Watching

- [x] 4.1 Add internal `WatchWithErrors`, clean shutdown classification, and the stable event-source-closed sentinel
- [x] 4.2 Preserve `Watch` as an event-only compatibility wrapper and test lazy subscription and early-break cleanup
- [x] 4.3 Expose the additive public method/sentinel with internal-error translation and facade tests
- [x] 4.4 Migrate `usbip-go watch` to the error-aware iterator and test subscription failure, unexpected closure, and clean cancellation
- [x] 4.5 Regenerate and verify the public API compatibility baseline

## 5. Specifications and Traceability

- [x] 5.1 Synchronize accepted requirements into main OpenSpec specs
- [x] 5.2 Update `openspec/TRACEABILITY.md` with precise source and regression evidence
- [x] 5.3 Update concise operator/developer documentation where the public watch and build-provenance behavior is described

## 6. Validation

- [x] 6.1 Run focused shell, Go, Bazel, race, and stamped-binary tests after the final edits
- [x] 6.2 Run format, strict lint, vulnerability, API, architecture, patch-coverage, and strict OpenSpec checks
  - Evidence: final format passed. The memory-safe strict-lint composite covered all 25 leaves: filtered
    `make lint` passed 23/23 while excluding only `golangci_lint` and `go_mod_check`, direct pinned
    golangci-lint reported zero issues, and the separate network-permitted `go_mod_check` passed 1/1.
    Fresh govulncheck passed 1/1; API, domain, and pure-Go checks passed; coverage passed at 93.88%
    (8248/8786); patch coverage passed at 100% (1439/1439); and strict validation passed for all 14/14
    main specs and 7/7 active changes.
- [x] 6.3 Run fresh uncached `make ci-local` through the Bazel entry point and record any external prerequisites
  - Evidence: the fresh, capped, uncached literal `make ci-local` completed through Bazel with exit 0:
    build passed; unit passed 23/23; race passed 23/23; conformance passed 1/1; lint passed 25/25;
    govuln passed 1/1; coverage passed 23/23 at 93.81% (8242/8786); release stamping passed 1/1; and GoReleaser
    configuration validation passed. Privileged USB/IP kernel integration remains an external prerequisite.
