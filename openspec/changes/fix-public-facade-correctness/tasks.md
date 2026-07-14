## 1. Deterministic Lifecycle Boundaries

- [x] 1.1 Serialize importer and exporter subscriber publication with closure and drain accepted terminal events before iterator return.
- [x] 1.2 Reserve Exporter serving state before context-aware listener creation and let Shutdown cancel setup before waiting.
- [x] 1.3 Give closed Importer state precedence over Attach validation while retaining the locked Close race recheck.

## 2. Public Configuration and Ownership

- [x] 2.1 Track accept-rate option presence, preserve explicit finite disable values, reject non-finite values, and translate the additive public sentinel.
- [x] 2.2 Add `BackoffFactory` and `WithImporterBackoffFactory`, invoke once per logical Attachment, preserve explicit per-call precedence, and serialize legacy shared custom strategies.
- [x] 2.3 Return a full canonical module-state map on every platform and check cancellation before each Linux observation.

## 3. Regression Coverage

- [x] 3.1 Add deterministic terminal importer/exporter buffer-drain tests without scheduler sleeps.
- [x] 3.2 Add closed/invalid Attach precedence and lazy backoff-factory invocation tests.
- [x] 3.3 Add explicit-zero and non-finite accept-rate construction and translation tests.
- [x] 3.4 Add pre-cancel and mid-Linux-probe full-shape cancellation tests.
- [x] 3.5 Add factory independence, per-call precedence, generation retention, and legacy custom-strategy concurrency tests.
- [x] 3.6 Add pre-bind shutdown/overlap and in-flight listener-setup cancellation tests.

## 4. Specifications, Documentation, and API Evidence

- [x] 4.1 Synchronize the accepted public-library-api, importer-lifecycle, exporter-daemon, operations-observability, and transport-networking specs.
- [x] 4.2 Update concise public and architecture documentation for backoff ownership, accept-rate validity, module-probe shape, and reservation-first ListenAndServe.
- [x] 4.3 Record change-local implementation/test trace evidence without editing the final combined `openspec/TRACEABILITY.md`.
- [x] 4.4 Prove the public API diff is additive and regenerate the API baseline through the repository entry point.

## 5. Capped Validation

- [x] 5.1 Run fresh serial focused normal tests for `internal/app` and `pkg/usbip` with constrained Go parallelism.
- [x] 5.2 Run focused race tests for subscriber, Serve/listener, backoff, and module-probe concurrency with constrained Go parallelism.
- [x] 5.3 Run repository formatting and verify generated Bazel source lists.
- [x] 5.4 Run strict validation for this change, every active change, and accepted main specs.
- [x] 5.5 Run targeted capped Go lint and API compatibility checks; report larger repository gates left to the final integrator.
  - API compatibility passed through the capped Make/Bazel target. Direct pinned
    golangci-lint v2.12.2 passed `./...` with one worker, a 1536 MiB Go memory
    limit, vendor mode, and the persistent repository cache. Larger combined
    repository gates remain assigned to the final integrator.
