## MODIFIED Requirements

### Requirement: Importer exposes remote listing, attach lifecycle, port listing, event watching, and close

Importer SHALL expose `ListRemote`, `Attach`, `Detach`, `ListPorts`, `Watch`, additive `WatchWithErrors`, and `Close`. `Watch` SHALL retain its v1 event-only behavior for source compatibility; `WatchWithErrors` SHALL yield `(Event, error)` pairs so consumers that require monitoring assurance can observe subscription and source failures.

#### Scenario: Remote devices are listed

- **WHEN** `Importer.ListRemote(ctx, endpoint)` is called
- **THEN** the importer dials the endpoint, sends `OP_REQ_DEVLIST`, decodes `OP_REP_DEVLIST`, and returns Devices

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
- **THEN** active port handles and background watchers are cancelled and drained
- **AND** repeated `Close()` calls are idempotent
