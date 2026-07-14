## MODIFIED Requirements

### Requirement: Daemon supports plain listener and systemd socket activation

`usbip-go serve` SHALL either bind its configured TCP address or consume a systemd-passed listener named for USB/IP service, and SHALL resolve ownership of every activation listener returned by systemd.

#### Scenario: Named activation listener exists

- **WHEN** `LISTEN_FDS` and `LISTEN_FDNAMES` provide exactly one listener named `usbip-go`
- **THEN** the daemon uses that listener
- **AND** closes every other activation listener returned in the same handoff
- **AND** status output identifies the listener as activation-received
- **AND** `--listen` is ignored

#### Scenario: Singleton activation listener has a different label

- **WHEN** systemd passes exactly one listener but its name is not `usbip-go`
- **THEN** the daemon accepts it as a singleton fallback
- **AND** logs a warning naming the observed and expected labels

#### Scenario: Multiple activation listeners are ambiguous

- **WHEN** systemd passes multiple listeners and none is named `usbip-go`
- **THEN** the daemon closes the passed listeners and fails startup rather than guessing

#### Scenario: Activation is unavailable

- **WHEN** the process is not started with socket activation
- **THEN** the daemon falls back to binding `--listen`

### Requirement: Status UDS exposes live daemon state

When `--status-socket` is non-empty, the daemon SHALL serve HTTP over a Unix-domain socket with a schema v1 status document and SHALL own the complete lifecycle of accepted status connections.

#### Scenario: Status root is requested

- **WHEN** an operator sends `GET /` over the status UDS
- **THEN** the response includes schema, version, commit, uptime, listener state, bound devices, active sessions, and kernel module state
- **AND** bound device listing failures are surfaced through `bound_devices_error` instead of being hidden as an empty list

#### Scenario: Status socket group is configured

- **WHEN** the status socket is created
- **THEN** the daemon applies mode `0660`
- **AND** group ownership from `--status-socket-group` is best-effort and non-fatal when lookup or chown fails

#### Scenario: Status server stops

- **WHEN** the daemon cancels the status server
- **THEN** active request contexts are canceled
- **AND** active and idle accepted HTTP connections are closed before the status server returns

### Requirement: Drain API is HTTP over the status UDS

The daemon SHALL expose a drain control path over the status socket that causes it to refuse new sessions, wait for in-flight sessions up to the daemon-side shutdown timeout, and exit. `runDaemon` SHALL own exactly one bounded `Exporter.Shutdown` call.

#### Scenario: Drain is requested

- **WHEN** the drain endpoint receives a valid POST
- **THEN** the first request starts the graceful drain path and returns HTTP 202 Accepted
- **AND** later drain requests return HTTP 200 OK without starting duplicate drain goroutines

#### Scenario: Drain request includes query parameters

- **WHEN** `POST /drain` includes any query string
- **THEN** the daemon rejects it with HTTP 400 because v1 defines no drain parameters

#### Scenario: Daemon drain attempt completes

- **WHEN** `Serve` exits because drain or process cancellation was requested
- **THEN** the status UDS remains available while the bounded `Exporter.Shutdown` call is in flight
- **AND** status shutdown and socket unlink occur only after that call returns or reaches its deadline
