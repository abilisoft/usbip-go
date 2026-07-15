## MODIFIED Requirements

### Requirement: Serve accepts one session goroutine per connection

Exporter Serve SHALL run an accept loop until context cancellation, shutdown,
or a permanent listener error, spawning a per-connection handler for each
accepted connection. A Session whose fd has been handed to the kernel SHALL
remain owned independently of the Serve context until authoritative peer
completion or Shutdown cleanup.

#### Scenario: Devlist request arrives

- **WHEN** a peer sends `OP_REQ_DEVLIST`
- **THEN** the handler lists exported devices, writes `OP_REP_DEVLIST`, and closes the userspace connection

#### Scenario: Import request arrives

- **WHEN** a peer sends `OP_REQ_IMPORT`
- **THEN** the handler decodes the BusID, registers a Session, writes the import reply, hands the fd to the kernel, and waits for Session termination

#### Scenario: Serve context is cancelled after kernel handoff

- **WHEN** Serve stops after a Session fd has been handed to the kernel
- **THEN** the Session remains registered and kernel-owned until peer completion or Shutdown claims cleanup

#### Scenario: Exporter process dies after stable kernel handoff

- **WHEN** the exporter process exits abruptly after both kernels own the handed-off socket
- **THEN** the exporter-side kernel session and exact client VHCI Port may remain claimed
- **AND** recovery first reconciles the exporter, writing `-1` to `usbip_sockfd` when that session remains used
- **AND** the exact client Port is free only when absent, `Null`, or `Available`; `NotAssigned` remains claimed
- **AND** a replacement daemon does not inherit the old session, so the client performs a fresh Attach
