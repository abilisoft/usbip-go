## MODIFIED Requirements

### Requirement: Detach is idempotent for port teardown

`Detach` SHALL cancel any reconnect watcher for a tracked Port, wait for watcher wind-down subject to the configured shutdown timeout, and share at most one active kernel detach attempt per tracked attachment generation or untracked kernel Port. A tracked handle SHALL be removed only after a successful attempt and only when the PortID still maps to that exact handle. When no handle or attach reservation exists, Detach SHALL reconcile the PortID against authoritative kernel state and detach it if it is live.

#### Scenario: Watcher is still running

- **WHEN** Detach is called while the reconnect watcher is active
- **THEN** the watcher is cancelled before kernel detach proceeds
- **AND** bounded waiting uses the AttachOptions ShutdownTimeout semantics

#### Scenario: Concurrent callers detach one attachment

- **WHEN** multiple callers overlap while detaching the same tracked generation or untracked kernel Port
- **THEN** exactly one caller issues the kernel detach
- **AND** other callers observe the same completed result

#### Scenario: Waiting detach caller is cancelled

- **WHEN** a follower's context is cancelled while the shared detach attempt continues
- **THEN** that follower returns its context error
- **AND** the owner and other followers continue observing the shared attempt

#### Scenario: Kernel detach fails

- **WHEN** the shared kernel detach attempt returns an error
- **THEN** every overlapping caller observes that error
- **AND** the exact tracked handle remains registered, or the ephemeral untracked attempt is removed, so a later call can retry

#### Scenario: PortID is reused before an old detach completes

- **WHEN** an old detach attempt completes after the PortID maps to a different attachment pointer
- **THEN** the old attempt does not remove the newer attachment's handle

#### Scenario: Port is already gone

- **WHEN** Detach observes that the kernel Port is absent or free
- **THEN** the result is classified with the canonical not-found/device sentinel
- **AND** no detach sysfs write occurs

#### Scenario: Port is live but untracked by this Importer

- **WHEN** Detach receives a PortID that authoritative kernel state reports as attached but the current Importer has no handle or reservation for it
- **THEN** Detach releases that kernel Port
- **AND** it does not fabricate reconnect metadata or a tracked handle

#### Scenario: Attach selects a Port under untracked detach

- **WHEN** AttachRemote selects a PortID whose untracked detach transition is active in the same Importer
- **THEN** the reservation is rejected before the adapter attach mutation

## ADDED Requirements

### Requirement: Port listing separates kernel truth from process metadata

Importer ListPorts SHALL preserve kernel-derived Port fields and SHALL enrich remote BusID and Remote only from the exact current handle generation whose used row still matches the last successful Port ID, DeviceID, and speed.

#### Scenario: Current Importer owns the live attachment

- **WHEN** ListPorts reads a used kernel row that matches the current handle generation
- **THEN** the returned Port retains kernel status, speed, DeviceID, and LocalBusID
- **AND** remote BusID and Remote are populated from that handle

#### Scenario: Attachment predates this Importer

- **WHEN** ListPorts reads a kernel row for which the current Importer has no matching live handle
- **THEN** remote BusID and Remote remain unknown
- **AND** LocalBusID remains the authoritative importer-local identity
