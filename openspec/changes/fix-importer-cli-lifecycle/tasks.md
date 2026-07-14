## 1. Regression Tests

- [x] 1.1 Add kernel-adapter tests that keep VHCI busids local and reject free or absent detach targets without a sysfs write
- [x] 1.2 Add importer tests for same-process metadata enrichment and truthful unknown remote metadata
- [x] 1.3 Add importer tests for untracked detach success, not-bound classification, shared concurrent results, retry, reservation exclusion, and Close coordination
- [x] 1.4 Add renderer tests that unknown Port remotes produce an empty string
- [x] 1.5 Correct the live CLI integration to capture the attach acknowledgement PortID and use it across separate port and detach processes
- [x] 1.6 Add event-mapper regressions for fail-once/succeed-on-retry discovery and coordinate validation, second-failure drop, no cross-event state reuse, and exporter-only topology bypass

## 2. Kernel and Importer Implementation

- [x] 2.1 Correct ListPorts and VHCI event mapping so only LocalBusID receives kernel-local identity
- [x] 2.2 Reconcile DetachPort status and the sysfs mutation under one fresh port-mutation snapshot
- [x] 2.3 Enrich ListPorts remote metadata only from the exact matching live handle generation
- [x] 2.4 Add deduplicated untracked detach coordination without weakening tracked handle, reservation, retry, or Close semantics
- [x] 2.5 Render unknown Port RemoteEndpoint values as empty in JSON, tables, and completion descriptions
- [x] 2.6 Retry failed VHCI topology discovery or coordinate validation at most once with a fully fresh event-local snapshot

## 3. Specifications and Documentation

- [x] 3.1 Update main OpenSpec requirements and `openspec/TRACEABILITY.md` with precise implementation and regression evidence
- [x] 3.2 Update public Port comments and JSON/operator documentation for truthful unknown remote metadata
- [x] 3.3 Validate the change and main specs with strict OpenSpec checks
- [x] 3.4 Update the main and delta kernel-adapter requirements and traceability for bounded event-local retry

## 4. Validation and Delivery

- [x] 4.1 Run focused unit, race, format, lint, patch-coverage, and API-compatibility validation
- [x] 4.2 Run the complete memory-capped KVM kernel integration with no unexpected skips and the env-gated real-busid scenarios
- [x] 4.3 Run the low-memory two-VM exporter/importer validation
- [ ] 4.4 Run fresh repository release gates, create and verify a signed Conventional Commit, open the PR, and verify every live GitHub check
- [x] 4.5 Run fresh focused unit, race, formatting, lint, strict OpenSpec, and 100% patch-coverage validation for the retry change
