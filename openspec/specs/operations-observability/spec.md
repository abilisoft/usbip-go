## Purpose

Specify daemon operations, status and health endpoints, structured logging, JSON contracts, deployment files, and diagnostics.

## Requirements

### Requirement: Daemon supports plain listener and systemd socket activation
`usbip-go serve` SHALL either bind its configured TCP address or consume a systemd-passed listener named for USB/IP service.

#### Scenario: Named activation listener exists
- **WHEN** `LISTEN_FDS` and `LISTEN_FDNAMES` provide exactly one listener named `usbip-go`
- **THEN** the daemon uses that listener
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
When `--status-socket` is non-empty, the daemon SHALL serve HTTP over a Unix-domain socket with a schema v1 status document.

#### Scenario: Status root is requested
- **WHEN** an operator sends `GET /` over the status UDS
- **THEN** the response includes schema, version, commit, uptime, listener state, bound devices, active sessions, and kernel module state
- **AND** bound device listing failures are surfaced through `bound_devices_error` instead of being hidden as an empty list

#### Scenario: Status socket group is configured
- **WHEN** the status socket is created
- **THEN** the daemon applies mode `0660`
- **AND** group ownership from `--status-socket-group` is best-effort and non-fatal when lookup or chown fails

### Requirement: Drain API is HTTP over the status UDS
The daemon SHALL expose a drain control path over the status socket that causes it to refuse new sessions, wait for in-flight sessions up to the daemon-side shutdown timeout, and exit.

#### Scenario: Drain is requested
- **WHEN** the drain endpoint receives a valid POST
- **THEN** the first request starts the graceful drain path and returns HTTP 202 Accepted
- **AND** later drain requests return HTTP 200 OK without starting duplicate drain goroutines

#### Scenario: Drain request includes query parameters
- **WHEN** `POST /drain` includes any query string
- **THEN** the daemon rejects it with HTTP 400 because v1 defines no drain parameters

### Requirement: Health endpoints are optional and separate from the USB/IP listener
When `--health-addr` is configured, the daemon SHALL serve `/healthz` and `/readyz` on that address using `net/http`.

#### Scenario: Liveness is checked
- **WHEN** `GET /healthz` reaches the health HTTP listener
- **THEN** the daemon returns 200 OK without inspecting kernel modules or the accept loop

#### Scenario: Readiness is checked
- **WHEN** `GET /readyz` runs
- **THEN** it returns 200 only when `usbip_core` and `usbip_host` are loaded, the listener is bound, the accept loop is armed, and any configured status socket is writable
- **AND** an empty status socket path is treated as no status-socket readiness gate

#### Scenario: Readiness probe stalls
- **WHEN** readiness collection exceeds its per-request timeout
- **THEN** `/readyz` returns 503 rather than hanging the HTTP request

### Requirement: JSON schema v1 is additively stable
All JSON-producing surfaces SHALL emit schema v1 envelopes and SHALL preserve field names, types, and semantics within schema v1.

#### Scenario: New field is added
- **WHEN** a JSON surface gains an additive field
- **THEN** existing consumers remain compatible by ignoring unknown fields

#### Scenario: Breaking JSON change is needed
- **WHEN** an existing field name, type, or meaning must change
- **THEN** a new schema envelope such as v2 is introduced and both versions coexist for at least one minor release

### Requirement: Structured logging is the primary operational signal
The daemon SHALL emit structured `slog` records with stable outcome fields for important lifecycle transitions instead of exposing a Prometheus metrics endpoint.

#### Scenario: Handshake is rejected by ACL
- **WHEN** the exporter rejects a peer due to CIDR allow-list policy
- **THEN** it logs a structured record with the closed-set rejection outcome

#### Scenario: Reconnect backs off
- **WHEN** the importer reconnect watcher sleeps before another attempt
- **THEN** it emits a structured log record with the reconnect outcome classification

### Requirement: Build provenance is visible at startup and in status
The binary SHALL expose version and commit metadata through `usbip-go version`, daemon startup logs, and status output where applicable.

#### Scenario: Daemon starts
- **WHEN** `usbip-go serve` starts
- **THEN** startup logs include version, commit, build date value, and Go version
- **AND** unstamped fields retain their compiled default values

### Requirement: Systemd units document operational defaults
The repository SHALL ship systemd service and socket units suitable for operator customization.

#### Scenario: Socket unit is enabled
- **WHEN** `usbip-go.socket` is enabled
- **THEN** systemd owns TCP port 3240 and starts the service on first inbound connection

#### Scenario: Service unit is customized
- **WHEN** operators deploy beyond localhost
- **THEN** they can add ACL, health, and hardening flags to `ExecStart`

#### Scenario: Service unit starts
- **WHEN** the packaged service unit starts
- **THEN** it attempts to `modprobe usbip_core` and `usbip_host` before launching the daemon
- **AND** missing modules do not fail unit startup when a custom kernel has built-in USB/IP support

#### Scenario: Service unit creates the status runtime directory
- **WHEN** the packaged service unit starts
- **THEN** systemd creates `/run/usbip-go` before the daemon binds the default status socket

#### Scenario: Boot-time module loading is configured
- **WHEN** operators install `contrib/modules-load.d/usbip-go.conf` under `/etc/modules-load.d`
- **THEN** `systemd-modules-load.service` loads `usbip_core`, `usbip_host`, and `vhci_hcd` at boot

### Requirement: Diagnostic docs support protocol bug reports
The repository SHALL document trace capture and fixture regeneration for USB/IP handshake issues.

#### Scenario: Operator files protocol bug
- **WHEN** a protocol-path bug is reported
- **THEN** the requested artifacts include pcap, trace-level daemon log, `usbip-go version`, status snapshot, and relevant `dmesg`

### Requirement: Drain cuts active USB traffic during graceful shutdown
The drain path SHALL prioritize daemon shutdown over preserving in-flight USB transfers.

#### Scenario: Active sessions exist during drain
- **WHEN** the daemon drains with active exporter sessions
- **THEN** it refuses new accepts
- **AND** requests kernel-side disconnect by writing `-1` to each session device's `usbip_sockfd`
- **AND** waits for accounted sessions up to `--shutdown-timeout`

#### Scenario: Kernel event does not arrive
- **WHEN** kernel disconnect succeeds but no detach/unbind event is observed
- **THEN** the daemon's session handle cancellation still unblocks the shutdown wait path

#### Scenario: Shutdown timeout elapses
- **WHEN** sessions remain after `--shutdown-timeout`
- **THEN** tracked session connections are force-closed and the daemon exits
