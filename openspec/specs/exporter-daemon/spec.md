## Purpose

Specify exporter-side device binding, USB/IP daemon session handling, resource limits, lifecycle events, and drain/shutdown behavior.

## Requirements

### Requirement: Exporter only requires exporter-side kernel modules
Exporter operations SHALL require `usbip_core` and `usbip_host`, but SHALL NOT require `vhci_hcd`.

#### Scenario: Exporter runs on a host without importer modules
- **WHEN** the host can export devices but does not load `vhci_hcd`
- **THEN** exporter bind, list, serve, and session-watch operations may proceed

### Requirement: Local and exported device listings are distinct
Exporter SHALL distinguish all local devices from devices visible to peers.

#### Scenario: Local inventory is requested
- **WHEN** `ListAvailable` or `usbip-go list --local` runs
- **THEN** every local USB Device visible through sysfs is returned regardless of bind state

#### Scenario: Wire-facing inventory is requested
- **WHEN** `ListExported` or an `OP_REQ_DEVLIST` peer query runs
- **THEN** only devices bound to `usbip_host` and not actively used by an importer are returned

### Requirement: Bind makes a device exportable without unsafe hub detachment
Bind SHALL claim a local Device under `usbip_host`, serialize per-BusID operations, and reject unsupported hub devices before destructive sysfs writes.

#### Scenario: Device is a USB hub
- **WHEN** Bind receives a BusID for a hub-class Device
- **THEN** Bind returns `ErrUnsupportedDevice`
- **AND** it does not detach the hub driver and cascade-disconnect downstream devices

#### Scenario: Concurrent bind and unbind target the same BusID
- **WHEN** operations race for one Device
- **THEN** the per-BusID lock serializes sysfs changes and prevents rollback from erasing a winning operation

### Requirement: Unbind returns a device to its original driver when possible
Unbind SHALL reverse a previous Bind and perform best-effort cleanup for bound state.

#### Scenario: Device is not bound
- **WHEN** Unbind is called for a Device not currently bound to `usbip_host`
- **THEN** the operation returns `ErrDeviceNotBound`

### Requirement: Serve accepts one session goroutine per connection
Exporter Serve SHALL run an accept loop until context cancellation, shutdown, or a permanent listener error, spawning a per-connection handler for each accepted connection.

#### Scenario: Devlist request arrives
- **WHEN** a peer sends `OP_REQ_DEVLIST`
- **THEN** the handler lists exported devices, writes `OP_REP_DEVLIST`, and closes the userspace connection

#### Scenario: Import request arrives
- **WHEN** a peer sends `OP_REQ_IMPORT`
- **THEN** the handler decodes the BusID, registers a Session, writes the import reply, hands the fd to the kernel, and waits for Session termination

### Requirement: Session registration enforces configured limits
The exporter SHALL enforce total session cap, per-peer cap, accept rate limit, handshake byte cap, and handshake timeout before handing a connection to the kernel.

#### Scenario: Total session cap is reached
- **WHEN** accepting another import would exceed MaxSessions
- **THEN** the exporter rejects the handshake before kernel handoff
- **AND** logs the closed-set rejection outcome

#### Scenario: Handshake stalls
- **WHEN** a peer does not complete the handshake within HandshakeTimeout
- **THEN** the exporter closes the connection and does not register a Session

### Requirement: Session IDs and counters are tracked on the exporter side
Each successful import Session SHALL have a UUIDv7 SessionID, remote address, BusID, start time, and byte counters where available.

#### Scenario: Sessions snapshot is requested
- **WHEN** `Exporter.Sessions(ctx)` or the status socket gathers sessions
- **THEN** it returns a point-in-time list sorted by start time

### Requirement: Session events are observable
`Exporter.WatchSessions` SHALL emit session lifecycle events while its context is live.

#### Scenario: Import handshake completes
- **WHEN** a Session is registered
- **THEN** a `session_started` event is emitted with Session details

#### Scenario: Session ends
- **WHEN** the kernel-owned connection closes, disconnects, or shutdown ends it
- **THEN** a `session_ended` event is emitted with the final Session snapshot and reason

### Requirement: Shutdown performs graceful drain
Exporter Shutdown SHALL stop new accepts, signal active sessions, wait for in-flight sessions subject to context/deadline semantics, and return a classified error on timeout or lifecycle misuse.

#### Scenario: Shutdown is called with active sessions
- **WHEN** active Sessions exist
- **THEN** the exporter refuses new handshakes and attempts graceful disconnect of existing Sessions

#### Scenario: Drain deadline expires
- **WHEN** the shutdown deadline expires before Sessions terminate
- **THEN** the exporter force-closes outstanding connections to unwedge handlers

### Requirement: Serve lifecycle rejects overlap and terminal reuse
Exporter SHALL reject overlapping Serve calls and treat completed Shutdown as terminal for that Exporter instance.

#### Scenario: Serve is already active
- **WHEN** a second Serve call starts on the same Exporter
- **THEN** it returns `ErrServeAlreadyRunning`

#### Scenario: Shutdown completed
- **WHEN** a caller tries to serve again on the same Exporter after Shutdown
- **THEN** it returns `ErrExporterShutdown`
