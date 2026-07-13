## MODIFIED Requirements

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
