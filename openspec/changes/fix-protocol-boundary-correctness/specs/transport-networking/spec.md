## MODIFIED Requirements

### Requirement: Static read and write deadlines are handshake-scoped

ReadDeadline and WriteDeadline SHALL set absolute deadlines on userspace handshake connections; the transport-configured read deadline SHALL remain authoritative unless a transport consumer explicitly clears or replaces it, and importer application logic SHALL NOT extend it from a later caller context deadline.

#### Scenario: Read deadline is configured

- **WHEN** a dialed or accepted connection is tuned
- **THEN** the read deadline is set to now plus the configured duration

#### Scenario: Caller context has a later deadline

- **WHEN** the transport has installed an earlier read deadline and an importer caller supplies a later context deadline
- **THEN** listing and import handshakes retain the earlier transport deadline

#### Scenario: Caller context is cancelled

- **WHEN** an importer handshake is blocked and its caller context is cancelled
- **THEN** application logic closes the connection to interrupt I/O without replacing the transport deadline
