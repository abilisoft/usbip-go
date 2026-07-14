## ADDED Requirements

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
