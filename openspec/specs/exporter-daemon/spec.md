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

- **WHEN** `ListAvailable` or `usbip-go list` runs
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

Exporter Serve SHALL run an accept loop until context cancellation, shutdown, or a permanent listener error, spawning a per-connection handler for each accepted connection. A Session whose fd has been handed to the kernel SHALL remain owned independently of the Serve context until authoritative peer completion or Shutdown cleanup.

#### Scenario: Devlist request arrives

- **WHEN** a peer sends `OP_REQ_DEVLIST`
- **THEN** the handler lists exported devices, writes `OP_REP_DEVLIST`, and closes the userspace connection

#### Scenario: Import request arrives

- **WHEN** a peer sends `OP_REQ_IMPORT`
- **THEN** the handler decodes the BusID, registers a Session, writes the import reply, hands the fd to the kernel, and waits for Session termination

#### Scenario: Serve context is cancelled after kernel handoff

- **WHEN** Serve stops after a Session fd has been handed to the kernel
- **THEN** the Session remains registered and kernel-owned until peer completion or Shutdown claims cleanup

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

`Exporter.WatchSessions` SHALL emit future session lifecycle events while its context is live and once the returned iterator is consumed. Exporter Session termination SHALL be detected by role-correct exporter lifecycle events or by an authoritative kernel activity probe. Importer-side VHCI Port events SHALL NOT terminate an exporter Session by BusID alone.

#### Scenario: Import handshake completes

- **WHEN** a Session is registered
- **THEN** a `session_started` event is emitted with Session details

#### Scenario: Session ends

- **WHEN** the kernel-owned connection closes, disconnects, or shutdown ends it
- **THEN** a `session_ended` event is emitted with the final Session snapshot and reason

#### Scenario: Normal peer completion emits no exporter detach event

- **WHEN** the kernel activity probe reports that a handed-off BusID is no longer used
- **THEN** the exporter ends and unregisters that Session without requiring a synthetic detach event

#### Scenario: Imported remote BusID collides with exported local BusID

- **WHEN** the shared kernel event stream emits an importer-side `PortDetachedEvent` whose remote BusID equals a live exporter Session's local BusID
- **THEN** the exporter ignores that event for Session termination
- **AND** retains the Session until a role-correct local unbind, authoritative inactive status, or Shutdown

#### Scenario: Watch iterator is constructed but not consumed

- **WHEN** a caller constructs a `WatchSessions` iterator and does not range over it
- **THEN** the exporter does not register a subscriber

#### Scenario: Watch iterator is consumed

- **WHEN** a caller ranges over a `WatchSessions` iterator before a session lifecycle event occurs
- **THEN** the caller receives subsequent `session_started` and `session_ended`
  events until its context is cancelled or the exporter shuts down

### Requirement: Shutdown performs graceful drain

Exporter Shutdown SHALL stop new accepts, signal active sessions, perform at most one required kernel Disconnect per handed-off Session, wait for in-flight Sessions and cleanup subject to context/deadline semantics, and return joined drain and cleanup failures. Repeated Shutdown calls SHALL observe the retained completion and error result without repeating Disconnect.

#### Scenario: Shutdown is called with active sessions

- **WHEN** active Sessions exist
- **THEN** the exporter refuses new handshakes and attempts graceful disconnect of every handed-off Session exactly once

#### Scenario: Multiple disconnects fail

- **WHEN** independent Session disconnect attempts return errors
- **THEN** Shutdown returns an error that preserves every failure for `errors.Is` inspection

#### Scenario: Shutdown is repeated after cleanup failure

- **WHEN** a completed Disconnect failed and Shutdown is called again
- **THEN** the repeat call returns the stored failure without another Disconnect attempt

#### Scenario: Drain deadline expires

- **WHEN** the shutdown deadline expires before Sessions terminate
- **THEN** the exporter force-closes outstanding connections to unwedge handlers
- **AND** returns a joined timeout and any cleanup failures already completed

### Requirement: Serve lifecycle rejects overlap and terminal reuse

Exporter SHALL reject overlapping Serve calls and treat completed Shutdown as terminal for that Exporter instance.

#### Scenario: Serve is already active

- **WHEN** a second Serve call starts on the same Exporter
- **THEN** it returns `ErrServeAlreadyRunning`

#### Scenario: Shutdown completed

- **WHEN** a caller tries to serve again on the same Exporter after Shutdown
- **THEN** it returns `ErrExporterShutdown`

### Requirement: Exporter terminal session events are drained

Exporter subscriber closure SHALL establish a publication barrier and an active
WatchSessions iterator SHALL drain every lifecycle event accepted into its
bounded subscriber buffer before terminal Exporter shutdown.

#### Scenario: Session end is queued before shutdown closes subscribers

- **WHEN** a session-ended event has entered a subscriber buffer before shutdown closes that subscriber
- **THEN** the active iterator yields the session-ended event before returning

#### Scenario: WatchSessions caller cancels independently

- **WHEN** the WatchSessions context is cancelled independently of Exporter shutdown
- **THEN** the iterator stops without a terminal-buffer drain requirement

### Requirement: Serving lifecycle is reserved before listener setup

The Exporter SHALL atomically reserve one Serve lifecycle before invoking a
listener factory, SHALL reject terminal or overlapping calls before factory
side effects, and SHALL let Shutdown cancel an in-flight context-aware factory.

#### Scenario: Listener setup overlaps Shutdown

- **WHEN** Shutdown begins while a listener factory is waiting on its supplied context
- **THEN** the factory context is cancelled
- **AND** Shutdown waits for the reserved Serve call to leave setup

#### Scenario: Concurrent Serve already owns the reservation

- **WHEN** a second Serve operation begins before the first Serve operation exits
- **THEN** the second operation returns `ErrServeAlreadyRunning`
- **AND** its listener factory is not invoked

### Requirement: Accept-rate option has explicit disable semantics

Exporter construction SHALL apply the default accept rate only when the option
is omitted, SHALL treat an explicit finite rate less than or equal to zero as
disabled, and SHALL reject a non-finite rate.

#### Scenario: Explicit zero differs from omission

- **WHEN** one Exporter omits the rate option and another explicitly supplies zero
- **THEN** the omitted option receives the documented default limiter
- **AND** the explicit zero Exporter has no accept-rate limiter

#### Scenario: Non-finite rate is configured

- **WHEN** NaN or either infinity is supplied as the accept rate
- **THEN** Exporter construction fails with the accept-rate-invalid sentinel
