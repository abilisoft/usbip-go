## 1. Wire Framing

- [x] 1.1 Add a deterministic live-stream regression proving an exact complete devlist frame returns without EOF or another byte
- [x] 1.2 Replace the trailing-byte probe with buffered-only detection and preserve adjacent trailing-byte diagnostics
- [x] 1.3 Replace body-only devlist fuzz seeds with valid full USB/IP frames and register any new focused test source in Bazel

## 2. VHCI Topology

- [x] 2.1 Remove successful cross-operation full and status topology caches while preserving fresh discovery error classification
- [x] 2.2 Pass one status-topology snapshot through AttachRemote status reading, free-port selection, and bounds validation
- [x] 2.3 Load a fresh topology for every relevant VHCI event while keeping exporter-only events independent of VHCI
- [x] 2.4 Add deterministic module-reload, attach-snapshot consistency, event-coordinate, delayed-event, and race regressions

## 3. Transport Deadline Ownership

- [x] 3.1 Add listing and import-handshake regressions that reject application SetReadDeadline calls and prove cancellation closes blocked reads
- [x] 3.2 Remove importer application deadline overrides while retaining connection-close cancellation watchers

## 4. Backoff Bounds

- [x] 4.1 Add deterministic tests for zero-bound v1 compatibility, final jitter capping, and near-duration-limit overflow safety
- [x] 4.2 Preserve non-negative public exponential bounds and cap jittered values at Max before duration conversion

## 5. RemoteEndpoint Grammar

- [x] 5.1 Add table-driven regressions for explicit empty ports, scoped IPv6, IPv6-only brackets, and 253-byte DNS bounds
- [x] 5.2 Preserve explicit-port presence during splitting, validate literals with netip, and enforce aggregate hostname length

## 6. Specification and Validation

- [x] 6.1 Synchronize modified behavior into the accepted main OpenSpec requirements
- [x] 6.2 Update `openspec/TRACEABILITY.md` with precise implementation and focused-test evidence
- [x] 6.3 Run formatting, focused unit and race tests, strict OpenSpec validation, relevant coverage/quality checks, and fresh repository validation
