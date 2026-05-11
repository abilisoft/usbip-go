## Purpose

Capture the public domain language and value-object behavior shared by the usbip-go library, CLI, daemon, and documentation.

## Requirements

### Requirement: Domain vocabulary is canonical
The project SHALL use the canonical terms Importer, Exporter, Device, BusID, RemoteEndpoint, Bind, Unbind, Handshake, URB traffic, Port, Attachment, Session, SessionID, Reconnect watcher, Drain, Event, and Wire in specs, docs, logs, and user-facing behavior.

#### Scenario: USB/IP roles are described
- **WHEN** code, docs, or CLI help describe the host that consumes a remote Device through `vhci_hcd`
- **THEN** the role is named Importer
- **AND** the role is not named client, consumer, or receiver in domain prose

#### Scenario: Export-side accounting is described
- **WHEN** a completed Handshake creates an exporter-side accounted unit
- **THEN** the unit is named Session
- **AND** importer-side kernel state is named Attachment instead of Session

### Requirement: BusID values are validated by context
The domain layer SHALL distinguish wire-tolerant BusID handling from strict operator-facing BusID validation.

#### Scenario: CLI receives a BusID
- **WHEN** an operator supplies a BusID to `attach`, `bind`, or `unbind`
- **THEN** the value is validated as a Linux topology identifier such as `1-1.2`
- **AND** invalid values surface `ErrBusIDInvalid` through the public facade

#### Scenario: Wire payload contains a BusID
- **WHEN** the wire decoder reads a NUL-padded BusID from a USB/IP frame
- **THEN** it accepts protocol-compatible encodings needed for upstream interop
- **AND** it maps semantically invalid BusIDs to the canonical sentinel error

### Requirement: RemoteEndpoint values normalize USB/IP peers
RemoteEndpoint SHALL represent an exporter peer as `host:port`, defaulting the port to 3240 when omitted and preserving valid IPv6 literal formatting.

#### Scenario: Port is omitted
- **WHEN** an operator or library caller supplies a remote host without a port
- **THEN** the parsed RemoteEndpoint uses TCP port 3240

#### Scenario: Endpoint is emitted
- **WHEN** a RemoteEndpoint is rendered in JSON or logs
- **THEN** the rendered value is stable `host:port` text with IPv6 literals bracketed

### Requirement: Device values model local and remote USB descriptors
Device SHALL carry USB descriptor fields, BusID, bus/dev numbers, speed, vendor/product IDs, class data, optional local string descriptors, and interface descriptors.

#### Scenario: Device is local
- **WHEN** a Device is discovered through local sysfs
- **THEN** Path, Manufacturer, Product, and interface alternate-setting data may be populated

#### Scenario: Device is remote
- **WHEN** a Device is decoded from `OP_REP_DEVLIST` or `OP_REP_IMPORT`
- **THEN** fields absent from the USB/IP wire format, such as local sysfs strings and interface alternate-setting, remain empty or zero

### Requirement: Speed and Status have forward-compatible string forms
Speed and Status SHALL render known kernel enum values with stable names and unknown values as numeric fallback strings.

#### Scenario: Known speed is rendered
- **WHEN** a known USB speed value is stringified
- **THEN** the output matches the documented long-form text used by JSON renderers

#### Scenario: Unknown enum value is rendered
- **WHEN** a future kernel or peer returns an unknown Speed or Status value
- **THEN** the value renders as `speed(N)` or `status(N)` instead of failing

### Requirement: SessionID is sortable UUIDv7
SessionID SHALL be generated as UUIDv7 using only the Go standard library in `pkg/domain`.

#### Scenario: New session is created
- **WHEN** the exporter completes a successful import Handshake
- **THEN** the resulting Session receives a unique UUIDv7 SessionID
- **AND** canonical string form is a 36-character UUID

### Requirement: Domain events are a closed public union
The domain event set SHALL consist of the current event kinds: `port_attached`, `port_detached`, `port_errored`, `port_reconnect_exhausted`, `device_bound`, `device_unbound`, `session_started`, and `session_ended`.

#### Scenario: Event is serialized
- **WHEN** an Event is emitted through watch JSON
- **THEN** the `kind` discriminator is one of the closed snake_case strings

#### Scenario: Reconnect attempts are exhausted
- **WHEN** the reconnect watcher reaches its configured maximum attempts without a successful reattach
- **THEN** it emits `port_reconnect_exhausted`
- **AND** the event includes the last viable Port snapshot, actual attempt count, and diagnostic last-error text

### Requirement: Domain package remains pure value objects
`pkg/domain` SHALL contain no I/O, goroutine orchestration, internal-package imports, or third-party dependencies.

#### Scenario: A dependency is added to pkg/domain
- **WHEN** CI evaluates the domain boundary rules
- **THEN** any third-party import or forbidden `internal/` import causes the architecture gate to fail
