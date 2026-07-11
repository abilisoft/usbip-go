## Purpose

Specify the stable Go API surface exported to embedders through `pkg/usbip` and `pkg/domain`.

## Requirements

### Requirement: pkg/usbip is the only public facade for services

External consumers SHALL construct Importer and Exporter services only through `pkg/usbip`, while pure data types remain aliases of `pkg/domain` values.

#### Scenario: Consumer imports the library

- **WHEN** a consumer wants to list, attach, bind, serve, or observe USB/IP behavior
- **THEN** they import `github.com/abilisoft/usbip-go/pkg/usbip`
- **AND** they do not import any package under `internal/`

#### Scenario: Consumer passes domain values

- **WHEN** a consumer mixes `usbip.Device` and `domain.Device`
- **THEN** the types are assignment-compatible because the public facade aliases value objects

### Requirement: Importer exposes remote listing, attach lifecycle, port listing, event watching, and close

Importer SHALL expose `ListRemote`, `Attach`, `Detach`, `ListPorts`, `Watch`, and `Close`.

#### Scenario: Remote devices are listed

- **WHEN** `Importer.ListRemote(ctx, endpoint)` is called
- **THEN** the importer dials the endpoint, sends `OP_REQ_DEVLIST`, decodes `OP_REP_DEVLIST`, and returns Devices

#### Scenario: Importer is closed

- **WHEN** `Importer.Close()` is called
- **THEN** active port handles and background watchers are cancelled and drained
- **AND** repeated `Close()` calls are idempotent

### Requirement: Attach options are per-call and defaultable

`AttachOptions` SHALL support `AutoReconnect`, `Backoff`, `MaxAttempts`, `OnReconnect`, `StatusPollInterval`, and `ShutdownTimeout`.

#### Scenario: AutoReconnect is enabled

- **WHEN** `Attach` succeeds with `AutoReconnect=true`
- **THEN** the importer starts a reconnect watcher for that Port

#### Scenario: Backoff is omitted

- **WHEN** AutoReconnect is enabled and no Backoff is supplied
- **THEN** reconnect attempts use the default exponential backoff with 1s minimum, 60s maximum, and 20% jitter

### Requirement: Backoff strategies are pluggable

The public API SHALL expose `BackoffStrategy`, `FixedBackoff`, `ExponentialBackoff`, and an explicitly panic-named `MustNewExponentialBackoff` constructor for reconnect timing. The v1 `NewExponentialBackoff` name remains as a deprecated compatibility alias.

#### Scenario: Exponential backoff is constructed

- **WHEN** `MustNewExponentialBackoff` receives invalid Min, Max, or Jitter values
- **THEN** construction panics because invalid backoff configuration is a programmer error

#### Scenario: Exponential backoff validation is fallible

- **WHEN** a caller invokes `ExponentialBackoffConfig.Validate` before construction
- **THEN** invalid configuration returns `ErrExponentialBackoffConfigInvalid` without panicking

#### Scenario: Custom backoff is supplied

- **WHEN** a caller passes a custom BackoffStrategy implementation
- **THEN** the facade adapts it to the internal reconnect machinery without exposing internal types

### Requirement: Exporter exposes local device, bind, serve, session, event, and shutdown operations

Exporter SHALL expose `ListAvailable`, `ListExported`, `Bind`, `Unbind`, `Serve`, `ListenAndServe`, `Sessions`, `WatchSessions`, and `Shutdown`.

#### Scenario: Local devices are listed

- **WHEN** `Exporter.ListAvailable(ctx)` is called
- **THEN** the exporter returns every local USB Device visible through the kernel adapter regardless of bind state

#### Scenario: Exported devices are listed

- **WHEN** `Exporter.ListExported(ctx)` is called
- **THEN** the exporter returns only devices bound to `usbip_host` and not actively claimed by an importer

### Requirement: Exporter options configure limits, ACLs, logging, timeouts, and transport tuning

Exporter construction SHALL accept role-specific options for logger, session caps, per-peer caps, accept rate, allow-list CIDRs, handshake byte cap, handshake timeout, shutdown timeout, and transport options.

#### Scenario: CIDR allow list is supplied

- **WHEN** at least one CIDR is configured
- **THEN** the exporter enforces fail-closed accept-path ACL behavior
- **AND** malformed CIDRs surface at construction time

#### Scenario: Resource limits are omitted

- **WHEN** no limit options are supplied
- **THEN** the exporter uses defaults of 128 total sessions, 8 sessions per peer, 10 accepts per second, 64 KiB handshake cap, and 10s handshake timeout

### Requirement: Transport options are validated before use

TransportOptions SHALL allow zero-valued kernel defaults and reject negative durations, probe counts, or buffer sizes at construction time.

#### Scenario: Importer transport tuning is set

- **WHEN** `WithImporterTransportOptions` is supplied
- **THEN** outbound dials receive the configured connect timeout, keepalive, buffer, and deadline values

#### Scenario: Exporter listener is owned by the facade

- **WHEN** `Exporter.ListenAndServe` binds through the transport adapter
- **THEN** accepted connections inherit exporter transport options

### Requirement: Public sentinel errors classify stable runtime failures

The facade SHALL expose stable sentinel errors for device, port, protocol, permission, kernel-module, lifecycle, and attach-option failures.

#### Scenario: Internal lifecycle error occurs

- **WHEN** an internal lifecycle sentinel would otherwise escape through the facade
- **THEN** `pkg/usbip` translates it to the corresponding public sentinel
- **AND** callers classify the result with `errors.Is` without importing `internal/app`

#### Scenario: Transport option validation fails

- **WHEN** Importer or Exporter construction receives negative TransportOptions values
- **THEN** construction returns an error before network I/O begins
- **AND** the error text identifies the invalid field

### Requirement: Public API compatibility is gated

Changes to `pkg/usbip` or `pkg/domain` SHALL be checked against API baselines and require an explicit breaking-change workflow for incompatible changes.

#### Scenario: Incompatible public API change is proposed

- **WHEN** CI runs API compatibility checks
- **THEN** the change fails unless the commit uses a Conventional Commit breaking marker (`!` in the subject or a `BREAKING CHANGE:` footer) and the relevant `api/*.json` baseline is regenerated
