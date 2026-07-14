## ADDED Requirements

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

## MODIFIED Requirements

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
