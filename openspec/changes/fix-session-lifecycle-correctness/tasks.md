## 1. Exporter Session Ownership

- [x] 1.1 Add a narrow exporter-activity capability and Linux adapter `usbip_status` probe, keeping the generated broad mock generator-owned through a focused wrapper test double.
- [x] 1.2 Keep post-handoff Session observation independent of Serve cancellation and add fake-clock status polling alongside lifecycle events.
- [x] 1.3 Implement per-session exactly-once cleanup completion/error storage and retained shutdown handle snapshots.
- [x] 1.4 Join Session drain, timeout, and every completed Disconnect failure for initial and repeated Shutdown calls.

## 2. Importer Detach Ownership

- [x] 2.1 Add one shared detach-attempt future per attachment handle with follower-local context cancellation and retry after failure.
- [x] 2.2 Recheck exact handle identity before detach and remove only the exact pointer after successful teardown.
- [x] 2.3 Serialize Linux adapter AttachRemote and DetachPort mutation paths through one VHCI lock, including reconnect rollback paths.
- [x] 2.4 Reserve the adapter-selected PortID before kernel mutation, atomically publish its exact handle, and preserve compensating teardown intent across bounded Detach waits.
- [x] 2.5 Carry the exact handle returned by reconnect Attach into rollback instead of rediscovering ownership through a reusable PortID.

## 3. Regression Coverage

- [x] 3.1 Add deterministic exporter tests for normal status-only completion, Serve cancellation, exactly-once late handoff cleanup, joined failures, timeout joining, and repeated Shutdown results.
- [x] 3.2 Add deterministic importer tests for concurrent shared success/failure, retry, follower cancellation, and PortID reuse without stale mutation or deletion.
- [x] 3.3 Add kernel adapter tests for activity status/error classification and attach/detach serialization without scheduler sleeps.
- [x] 3.4 Add deterministic importer and adapter regressions for Detach during reserved-port handoff, bounded wait expiry, late compensation, and reservation rejection before sysfs mutation.
- [x] 3.5 Add a deterministic regression proving reconnect rollback cannot race replacement-port compensation.
- [x] 3.6 Add a BusID-collision regression proving importer Port events cannot terminate exporter Sessions.
- [x] 3.7 Add deterministic same-PortID reservation-wins/detach-wins regressions with compensation failure, retry, and Close coverage.
- [x] 3.8 Add an immediate second-failure regression proving backoff Reset precedes replacement-watcher Next, including race execution.
- [x] 3.9 Strengthen adapter serialization coverage so concurrent Detach is blocked from discovery through attach mutation.
- [x] 3.10 Add a deterministic same-PortID regression proving old rollback cannot detach or remove a newer replacement generation.

## 4. Specifications and Validation

- [x] 4.1 Synchronize accepted exporter-daemon, importer-lifecycle, and kernel-adapter specs with the implemented behavior.
- [x] 4.2 Update `openspec/TRACEABILITY.md` with exact implementation and regression-test evidence.
- [x] 4.3 Run formatting, focused unit tests, focused race tests, strict OpenSpec validation, lint, patch coverage, and fresh repository validation appropriate to the change.
