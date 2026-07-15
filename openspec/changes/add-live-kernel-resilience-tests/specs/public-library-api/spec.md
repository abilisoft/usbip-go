## MODIFIED Requirements

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
