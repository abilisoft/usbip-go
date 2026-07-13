## 1. Daemon and Status Lifecycle

- [x] 1.1 Make `runDaemon` own one bounded exporter shutdown and preserve the status UDS until it returns
- [x] 1.2 Cancel active status handlers and close active or idle accepted connections before `serveStatus` returns
- [x] 1.3 Close every unselected systemd activation listener

## 2. Drain and History Safety

- [x] 2.1 Reject non-2xx, wrong-schema, omitted, or null required drain status data
- [x] 2.2 Serialize history updates with a private flock and atomically replace the complete file
- [x] 2.3 Correct permissive existing history and lock-file modes

## 3. Specification and Documentation

- [x] 3.1 Synchronize accepted CLI and operations OpenSpec requirements
- [x] 3.2 Update operator/schema documentation and precise traceability evidence

## 4. Validation

- [x] 4.1 Run focused normal, race, and repeated concurrency regressions
- [x] 4.2 Run formatting, targeted strict lint, and strict OpenSpec validation
- [x] 4.3 Run full unit, race, coverage, API, domain, full lint, and 100% patch-coverage validation
  - Evidence: fresh unit and race suites passed 23/23 each; coverage passed at 93.74% (8236/8786),
    patch coverage passed at 100% (1417/1417), and API and domain checks passed. The memory-safe full-lint
    composite covered all 25 leaves: filtered `make lint` passed 23/23 while excluding only
    `golangci_lint` and `go_mod_check`, direct pinned golangci-lint reported zero issues, and the exact
    `go_mod_check` target passed separately.
- [x] 4.4 Run the explicitly `requires-network` `go_mod_check` when network validation is permitted
  - Evidence: the network-permitted `go_mod_check` passed 1/1 in 21.6 seconds.
