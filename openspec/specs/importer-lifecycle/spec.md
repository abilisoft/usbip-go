## Purpose

Specify importer-side behavior for listing remote devices, attaching devices to vhci ports, reconnecting, detaching, and watching port/device events.

## Requirements

### Requirement: Importer only requires importer-side kernel modules
Importer operations SHALL require `usbip_core` and `vhci_hcd`, but SHALL NOT require `usbip_host`.

#### Scenario: Importer starts on a host without exporter modules
- **WHEN** the host has importer-side kernel support but does not load `usbip_host`
- **THEN** importer operations may proceed
- **AND** exporter-only module absence does not block Importer construction

### Requirement: Remote listing uses the USB/IP devlist handshake
`ListRemote` SHALL open a TCP connection, send `OP_REQ_DEVLIST`, decode `OP_REP_DEVLIST`, and close the connection after the reply.

#### Scenario: Remote has no devices
- **WHEN** `OP_REP_DEVLIST` reports zero devices
- **THEN** the importer returns an empty device list without error

#### Scenario: Peer sends malformed devlist data
- **WHEN** the devlist reply violates the wire format
- **THEN** the importer returns the appropriate protocol sentinel or wrapped I/O error

### Requirement: Attach performs a complete userspace handshake before kernel handoff
`Attach` SHALL dial the RemoteEndpoint, send `OP_REQ_IMPORT`, decode `OP_REP_IMPORT`, and pass the connected socket fd plus decoded device spec to `vhci_hcd`.

#### Scenario: Attach succeeds
- **WHEN** the remote exporter accepts the requested BusID
- **THEN** the importer hands the socket to the kernel
- **AND** returns the occupied Port

#### Scenario: Remote rejects the BusID
- **WHEN** `OP_REP_IMPORT` has a non-zero status indicating unavailable, busy, or missing device
- **THEN** the importer maps the reply to the canonical public sentinel

### Requirement: Concurrent attach attempts are deduplicated per remote BusID
The importer SHALL reject overlapping Attach calls for the same `(RemoteEndpoint, BusID)` pair.

#### Scenario: Two goroutines attach the same remote device
- **WHEN** one Attach is already in-flight for a normalized remote and BusID
- **THEN** a concurrent duplicate Attach returns `ErrAttachInProgress`
- **AND** the device is not imported onto two local Ports by this process

### Requirement: Kernel port allocation is race-safe
Importer kernel handoff SHALL serialize the free-port discovery and sysfs attach write critical section.

#### Scenario: Two different attaches need a free port
- **WHEN** multiple AttachRemote calls run concurrently
- **THEN** only one caller can claim a given free vhci Port
- **AND** a loser observes a canonical no-free-port or kernel error instead of corrupting state

### Requirement: Detach is idempotent for port teardown
`Detach` SHALL cancel any reconnect watcher for the Port, wait for watcher wind-down subject to the configured shutdown timeout, and request kernel detach.

#### Scenario: Watcher is still running
- **WHEN** Detach is called while the reconnect watcher is active
- **THEN** the watcher is cancelled before kernel detach proceeds
- **AND** bounded waiting uses the AttachOptions ShutdownTimeout semantics

#### Scenario: Port is already gone
- **WHEN** Detach observes that the kernel Port is absent
- **THEN** the result is classified with the canonical not-found/device sentinel

### Requirement: Reconnect watcher re-establishes attachments
When AutoReconnect is enabled, the importer SHALL observe detach signals, run reconnect attempts with configured backoff, and preserve Port lifecycle semantics.

#### Scenario: Connection drops
- **WHEN** a watched attachment is detached unexpectedly
- **THEN** the watcher retries Handshake and kernel handoff for the same RemoteEndpoint and BusID
- **AND** it invokes OnReconnect before each retry when configured

#### Scenario: Reconnect succeeds
- **WHEN** a retry completes successfully
- **THEN** the watcher resets backoff state
- **AND** updates its last-known Port snapshot

### Requirement: Stale reconnect events are rejected
Reconnect watcher state SHALL include per-Port generation tokens so stale uevents cannot resurrect an old handle after a newer attach state exists.

#### Scenario: Stale detach event arrives
- **WHEN** a kernel event references a generation older than the current handle
- **THEN** the watcher ignores the stale event

### Requirement: Watch merges kernel and application port events
`Importer.Watch` SHALL expose a single event sequence for port and device lifecycle events from kernel uevents plus application-synthesized reconnect exhaustion.

#### Scenario: Slow subscriber is present
- **WHEN** a watch subscriber cannot keep up with fan-out
- **THEN** events may be dropped for that subscriber rather than back-pressuring the kernel event dispatcher

#### Scenario: Watch context is cancelled
- **WHEN** the caller cancels the Watch context or stops iteration
- **THEN** subscription resources are released and the sequence terminates
