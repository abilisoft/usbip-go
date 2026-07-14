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

`ListRemote` SHALL open a TCP connection, send `OP_REQ_DEVLIST`, decode the complete count-delimited `OP_REP_DEVLIST` without waiting for a subsequent byte, preserve any tighter transport-configured read deadline, and close the connection after the reply.

#### Scenario: Remote has no devices

- **WHEN** `OP_REP_DEVLIST` reports zero devices on a connection that remains open
- **THEN** the importer returns an empty device list without waiting for EOF or another byte

#### Scenario: Peer sends malformed devlist data

- **WHEN** the devlist reply violates the wire format
- **THEN** the importer returns the appropriate protocol sentinel or wrapped I/O error

#### Scenario: Transport deadline is tighter than caller deadline

- **WHEN** ListRemote receives a connection with an earlier configured read deadline than the context deadline
- **THEN** ListRemote does not replace or extend the connection deadline

#### Scenario: Remote listing is cancelled while blocked

- **WHEN** ListRemote is blocked reading the reply and the caller context is cancelled
- **THEN** the connection is closed to interrupt the read without replacing its deadline

### Requirement: Attach performs a complete userspace handshake before kernel handoff

`Attach` SHALL dial the RemoteEndpoint, send `OP_REQ_IMPORT`, decode `OP_REP_IMPORT` without replacing the transport-configured read deadline, and pass the connected socket fd plus decoded device spec to `vhci_hcd`.

#### Scenario: Attach succeeds

- **WHEN** the remote exporter accepts the requested BusID
- **THEN** the importer hands the socket to the kernel
- **AND** returns the occupied Port

#### Scenario: Remote rejects the BusID

- **WHEN** `OP_REP_IMPORT` has a non-zero status indicating unavailable, busy, or missing device
- **THEN** the importer maps the reply to the canonical public sentinel

#### Scenario: Transport deadline is tighter than caller deadline

- **WHEN** Attach receives a connection with an earlier configured read deadline than the context deadline
- **THEN** Attach does not replace or extend the connection deadline before decoding the reply

#### Scenario: Attach handshake is cancelled while blocked

- **WHEN** Attach is blocked reading the reply and the caller context is cancelled
- **THEN** the connection is closed to interrupt the read without replacing its deadline
- **AND** kernel handoff does not run

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

### Requirement: Attach handoff publication is detach-safe

After the kernel adapter selects a local PortID, the importer SHALL reserve that PortID before the kernel attachment can become live and SHALL atomically replace the reservation with the exact attachment handle after successful handoff. `Detach` SHALL wait for a matching reservation outside the importer-wide mutex, subject to the attachment shutdown timeout and caller context, and SHALL preserve teardown intent if that wait ends before publication.

#### Scenario: Detach overlaps live handoff before handle publication

- **WHEN** Detach targets a reserved PortID after kernel handoff begins but before its handle is published
- **THEN** Detach does not return the not-found/device sentinel for the publication gap
- **AND** successful publication leads to one shared teardown attempt for the exact handle
- **AND** every waiting caller observes that attempt's result even if successful teardown removes the handle before the waiter resumes

#### Scenario: Publication wait expires before handoff returns

- **WHEN** Detach reaches its shutdown bound or caller cancellation while a reserved handoff remains incomplete
- **THEN** Detach returns the applicable timeout or context error without holding an importer-wide lock
- **AND** a later successful publication starts compensating teardown while retaining the exact handle on teardown failure

#### Scenario: Reconnect rollback overlaps replacement teardown

- **WHEN** old-handle reconnect rollback and a Detach-requested compensation both target the newly published replacement generation
- **THEN** they share one active kernel detach ownership
- **AND** a failed compensation is retained for a later explicit retry rather than being raced by an automatic rollback retry

#### Scenario: Reconnect rollback sees a reused replacement PortID

- **WHEN** a reconnect Attach returns a replacement generation, that exact generation is removed, and its PortID is reused before old-handle rollback begins
- **THEN** rollback does not issue kernel detach against the newer generation
- **AND** the newer generation remains registered as the current PortID owner

#### Scenario: Same-PortID replacement reservation overlaps old Detach

- **WHEN** reconnect reserves the old handle's PortID before publishing its replacement and Detach concurrently targets that PortID
- **THEN** the reservation wins the per-Port transition and records teardown intent for the replacement generation
- **AND** the old Detach does not wait behind AttachRemote and then mutate the newly-live replacement
- **AND** failed replacement compensation retains the exact replacement handle for explicit retry

#### Scenario: Old Detach wins before same-PortID reservation

- **WHEN** Detach claims the old handle generation before reconnect reserves that PortID
- **THEN** the later reservation is rejected before the adapter's kernel attach mutation

### Requirement: Detach is idempotent for port teardown

`Detach` SHALL cancel any reconnect watcher for the Port, wait for watcher wind-down subject to the configured shutdown timeout, and share at most one active kernel detach attempt per attachment generation. The handle SHALL be removed only after a successful attempt and only when the PortID still maps to that exact handle.

#### Scenario: Watcher is still running

- **WHEN** Detach is called while the reconnect watcher is active
- **THEN** the watcher is cancelled before kernel detach proceeds
- **AND** bounded waiting uses the AttachOptions ShutdownTimeout semantics

#### Scenario: Concurrent callers detach one attachment

- **WHEN** multiple callers overlap while detaching the same handle generation
- **THEN** exactly one caller issues the kernel detach
- **AND** other callers observe the same completed result

#### Scenario: Waiting detach caller is cancelled

- **WHEN** a follower's context is cancelled while the shared detach attempt continues
- **THEN** that follower returns its context error
- **AND** the owner and other followers continue observing the shared attempt

#### Scenario: Kernel detach fails

- **WHEN** the shared kernel detach attempt returns an error
- **THEN** every overlapping caller observes that error
- **AND** the exact handle remains registered so a later call can retry

#### Scenario: PortID is reused before an old detach completes

- **WHEN** an old detach attempt completes after the PortID maps to a different attachment pointer
- **THEN** the old attempt does not remove the newer attachment's handle

#### Scenario: Port is already gone

- **WHEN** Detach observes that the kernel Port is absent
- **THEN** the result is classified with the canonical not-found/device sentinel

### Requirement: Reconnect watcher re-establishes attachments

When AutoReconnect is enabled, the importer SHALL observe detach signals, run reconnect attempts with configured backoff, and preserve Port lifecycle semantics.

#### Scenario: Connection drops

- **WHEN** a watched attachment is detached unexpectedly
- **THEN** the watcher retries Handshake and kernel handoff for the same RemoteEndpoint and BusID
- **AND** it queues an OnReconnect notification before each retry when configured
- **AND** callback invocations run serially without stalling retry cadence
- **AND** pending notifications MAY coalesce to the latest attempt when the callback is slower than retries

#### Scenario: Reconnect succeeds

- **WHEN** a retry completes successfully
- **THEN** the completing watcher resets backoff state before starting the replacement watcher
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

### Requirement: Importer terminal event publication is drained

Importer subscriber closure SHALL establish a publication barrier and an active
watch iterator SHALL drain every application event accepted into its bounded
subscriber buffer before terminal Importer closure.

#### Scenario: Reconnect exhaustion is queued before Importer closure

- **WHEN** a reconnect-exhausted event has entered a subscriber buffer before `Importer.Close` closes that subscriber
- **THEN** the active iterator yields the reconnect-exhausted event before returning

#### Scenario: Caller cancels event watching

- **WHEN** the Watch context is cancelled independently of Importer closure
- **THEN** the iterator stops without a terminal-buffer drain requirement

### Requirement: Backoff factory state follows one logical Attachment

An auto-reconnecting Attach SHALL construct importer-level backoff-factory state
at most once after terminal-state, argument-validation, and duplicate-Attachment
checks pass but before kernel or network work begins. Successful reconnect
replacement generations SHALL retain that same strategy.

#### Scenario: Attach is rejected before reservation

- **WHEN** Attach is rejected because the Importer is closed or its arguments are invalid
- **THEN** its configured backoff factory is not invoked

#### Scenario: Reconnect creates a replacement generation

- **WHEN** an Attachment successfully reconnects and starts a replacement watcher
- **THEN** the replacement watcher uses the original logical Attachment's strategy
- **AND** the factory invocation count remains one

### Requirement: Closed Importer state precedes Attach validation

After Close, Importer Attach SHALL return `ErrImporterClosed` before validating
the endpoint, BusID, or AttachOptions, while the locked attachment reservation
SHALL recheck closure against a racing Close.

#### Scenario: Closed Attach receives malformed inputs

- **WHEN** Attach is called after Close with a malformed endpoint, BusID, or negative MaxAttempts
- **THEN** it returns `ErrImporterClosed`
- **AND** no backoff factory, kernel, or network operation runs
