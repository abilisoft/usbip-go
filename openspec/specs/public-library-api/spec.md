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

Importer SHALL expose `ListRemote`, `Attach`, `Detach`, `ListPorts`, `Watch`,
additive `WatchWithErrors`, and `Close`. `Detach` SHALL accept a valid
kernel-owned Port created by an earlier Importer even when the current Importer
has no process-local handle. `ListPorts` SHALL expose the normalized kernel
capacity view, including free rows, rather than applying the CLI active-only
presentation filter when the VHCI snapshot is usable. It SHALL return an error
and no partial or synthetic Ports for Linux's controller-not-ready placeholder,
while preserving ordinary claimed `NotAssigned` rows. `Watch` SHALL retain its
v1 event-only behavior for source compatibility; `WatchWithErrors` SHALL yield
`(Event, error)` pairs so consumers that require monitoring assurance can
observe subscription and source failures.

#### Scenario: Remote devices are listed

- **WHEN** `Importer.ListRemote(ctx, endpoint)` is called
- **THEN** the importer dials the endpoint, sends `OP_REQ_DEVLIST`, decodes `OP_REP_DEVLIST`, and returns Devices

#### Scenario: Fresh Importer detaches a kernel-owned Port

- **WHEN** one Importer hands an attachment to the kernel and a fresh Importer later calls `Detach` for that Port
- **THEN** the fresh Importer delegates to the authoritative kernel detach operation
- **AND** no public method signature changes

#### Scenario: Port listing exposes kernel capacity

- **WHEN** `Importer.ListPorts(ctx)` reads normalized kernel Port rows
- **THEN** it may return both active attachments and free capacity rows
- **AND** callers can distinguish them through `Port.Status`
- **AND** ordinary `NotAssigned` rows remain visible as claimed Ports
- **AND** a controller-not-ready placeholder returns an error with no partial or synthetic Ports

#### Scenario: Error-aware event watching is used

- **WHEN** a consumer ranges over `Importer.WatchWithErrors(ctx)`
- **THEN** ordinary events are yielded with a nil error
- **AND** subscription or unexpected source failures are yielded as errors

#### Scenario: Compatibility event watching is used

- **WHEN** an existing consumer ranges over `Importer.Watch(ctx)`
- **THEN** it receives the same event-only sequence shape as v1
- **AND** source failure terminates that compatibility iterator without changing its method signature

#### Scenario: Importer is closed

- **WHEN** `Importer.Close()` is called
- **THEN** active port handles, detach mutations, and background watchers are cancelled or drained as applicable
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

The public API SHALL expose `BackoffStrategy`, `FixedBackoff`, `ExponentialBackoff`, and an explicitly panic-named `MustNewExponentialBackoff` constructor for reconnect timing. Exponential Min and Max SHALL remain non-negative for v1 compatibility, and every jittered exponential delay SHALL remain no greater than Max without duration overflow. The v1 `NewExponentialBackoff` name remains as a deprecated compatibility alias.

#### Scenario: Exponential backoff is constructed

- **WHEN** `MustNewExponentialBackoff` receives negative Min or Max, Max below Min, or invalid Jitter values
- **THEN** construction panics because invalid backoff configuration is a programmer error

#### Scenario: Exponential backoff validation is fallible

- **WHEN** a caller invokes `ExponentialBackoffConfig.Validate` before construction
- **THEN** invalid configuration returns `ErrExponentialBackoffConfigInvalid` without panicking

#### Scenario: Jitter samples above the capped base

- **WHEN** an exponential delay has reached Max and positive jitter would produce a larger or unrepresentable duration
- **THEN** `Next` returns Max

#### Scenario: Zero bounds retain v1 behavior

- **WHEN** Min is zero with a non-negative Max, including an all-zero bound pair
- **THEN** validation and construction succeed
- **AND** `Next` retains the historical zero-delay schedule

#### Scenario: Custom backoff is supplied

- **WHEN** a caller passes a custom BackoffStrategy implementation
- **THEN** the facade adapts it to the internal reconnect machinery without exposing internal types

#### Scenario: Fixed zero-delay backoff is supplied

- **WHEN** a caller explicitly supplies `FixedBackoff{Delay: 0}`
- **THEN** the fixed strategy retains its deterministic immediate-retry behavior

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

### Requirement: Reconnect backoff ownership is configurable without changing AttachOptions

The public API SHALL expose `BackoffFactory` and
`WithImporterBackoffFactory` as an additive importer-level default, SHALL keep
the public `AttachOptions` field layout unchanged, and SHALL preserve the legacy
`WithImporterBackoff` option.

#### Scenario: Stateful strategy factory is configured

- **WHEN** two top-level auto-reconnecting Attach calls use an importer configured with `WithImporterBackoffFactory`
- **THEN** the factory is invoked once for each logical Attachment
- **AND** each Attachment owns the returned strategy through all of its replacement generations

#### Scenario: Per-Attach strategy is explicit

- **WHEN** `AttachOptions.Backoff` and an importer-level backoff factory are both configured
- **THEN** the explicit per-Attach strategy wins
- **AND** the importer-level factory is not invoked for that Attachment

#### Scenario: Nil factory restores the library default

- **WHEN** `WithImporterBackoffFactory(nil)` follows either `WithImporterBackoff` or a non-nil `WithImporterBackoffFactory`
- **THEN** the prior importer-level reconnect strategy configuration is cleared
- **AND** Attach uses the library-default exponential backoff

#### Scenario: Legacy custom strategy is configured

- **WHEN** multiple Attachments use one custom strategy supplied through `WithImporterBackoff`
- **THEN** calls into that shared strategy are serialized

### Requirement: Public accept-rate configuration preserves presence and validity

`WithExporterAcceptRateLimit` SHALL distinguish omission from an explicit
finite value and public construction SHALL expose
`ErrAcceptRateLimitInvalid` for a non-finite value.

#### Scenario: Accept-rate option is omitted

- **WHEN** an Exporter is constructed without `WithExporterAcceptRateLimit`
- **THEN** the documented library default rate is applied

#### Scenario: Accept-rate option explicitly disables limiting

- **WHEN** an Exporter is constructed with a finite rate less than or equal to zero
- **THEN** accept-rate limiting is disabled

#### Scenario: Accept-rate option is non-finite

- **WHEN** an Exporter is constructed with NaN or either infinity as its accept rate
- **THEN** construction fails with `ErrAcceptRateLimitInvalid`
- **AND** no adapter construction or listener side effect occurs

### Requirement: Public lifecycle errors have deterministic precedence

Terminal and overlapping object lifecycle state SHALL be rejected before
validation or transport side effects at the public facade.

#### Scenario: Attach is called after Importer Close

- **WHEN** `Importer.Attach` is called after `Importer.Close` with otherwise invalid arguments
- **THEN** it returns `ErrImporterClosed`
- **AND** it does not return an argument-validation error

#### Scenario: ListenAndServe is called after Exporter Shutdown

- **WHEN** `Exporter.ListenAndServe` is called after `Exporter.Shutdown`
- **THEN** it returns `ErrExporterShutdown`
- **AND** it does not invoke the transport listener factory

#### Scenario: ListenAndServe overlaps Serve

- **WHEN** `Exporter.ListenAndServe` overlaps an active Serve call
- **THEN** it returns `ErrServeAlreadyRunning`
- **AND** it does not invoke the transport listener factory

### Requirement: Public kernel module probing is shape-stable under cancellation

`ProbeKernelModules` SHALL always return entries for `usbip_core`, `vhci_hcd`,
and `usbip_host` on every platform and on every cancellation path.

#### Scenario: Probe context is already cancelled

- **WHEN** `ProbeKernelModules` receives a cancelled context
- **THEN** it returns an error preserving the context cause
- **AND** all three canonical module keys are present with `Unknown` state

#### Scenario: Linux probe is cancelled after a completed observation

- **WHEN** cancellation occurs between Linux module observations
- **THEN** completed observations remain in the result
- **AND** every unprobed canonical key remains present with `Unknown` state
