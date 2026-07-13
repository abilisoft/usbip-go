## MODIFIED Requirements

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
- **THEN** the caller receives subsequent `session_started` and `session_ended` events until its context is cancelled or the exporter shuts down

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

- **WHEN** the shutdown deadline expires before Sessions terminate or cleanup completes
- **THEN** the exporter force-closes outstanding connections to unwedge handlers
- **AND** returns a joined timeout and any cleanup failures already completed
