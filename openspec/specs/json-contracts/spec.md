## Purpose

Define schema-v1 JSON contracts emitted by the CLI, watch streams, and daemon status socket.

## Requirements

### Requirement: JSON documents are schema-versioned

Every top-level JSON document and watch jsonlines record SHALL include `"schema": "v1"` as the leading envelope field.

#### Scenario: JSON renderer emits a document

- **WHEN** a CLI command writes JSON
- **THEN** the first serialized field is `schema`
- **AND** its value is `v1`

### Requirement: Device view has stable field names

Device JSON views SHALL include `busid`, `busnum`, `devnum`, `speed`, `vendor_id`, and `product_id`.

#### Scenario: Device IDs are rendered

- **WHEN** vendor and product IDs are serialized in device views
- **THEN** they are lowercase four-digit hexadecimal strings without `0x`

### Requirement: Port view has stable field names

Port JSON views SHALL include `id`, `status`, `speed`, `remote`, `busid`, and `local_busid`. `busid` and `remote` SHALL describe exporter-side identity only when known; they SHALL NOT be populated from importer-local VHCI topology.

#### Scenario: Local BusID is unknown

- **WHEN** the kernel has not mapped a local BusID for a Port
- **THEN** `local_busid` is the empty string instead of being omitted

#### Scenario: Remote attachment metadata is unavailable

- **WHEN** a fresh Importer or CLI process lists a kernel-owned attachment without matching process-local metadata
- **THEN** `remote` and `busid` are empty strings instead of being omitted
- **AND** `remote` is not rendered as `:3240`
- **AND** `local_busid` retains the importer-local sysfs identity

### Requirement: Session view has stable field names

Session JSON views SHALL include `id`, `remote`, `busid`, `started_at`, `bytes_in`, and `bytes_out`.

#### Scenario: Session start time is serialized

- **WHEN** a Session is rendered
- **THEN** `started_at` is RFC3339Nano UTC text

### Requirement: List envelopes wrap homogeneous collections

List-style JSON outputs SHALL wrap their collections in schema-v1 envelopes.

#### Scenario: Devices are listed

- **WHEN** `list` or `list HOST` succeeds in JSON mode
- **THEN** stdout has shape `{ "schema": "v1", "devices": [...] }`

#### Scenario: Ports are listed

- **WHEN** `port` succeeds in JSON mode
- **THEN** stdout has shape `{ "schema": "v1", "ports": [...] }`

### Requirement: Mutating command acknowledgements are success-only

Successful mutating commands SHALL emit ack envelopes with `schema`, `op`, `ok: true`, and op-specific payload; failures SHALL use stderr and non-zero exit codes.

#### Scenario: Attach succeeds

- **WHEN** `attach --output=json` succeeds
- **THEN** stdout includes `op: "attach"`, `ok: true`, and a `port` view

#### Scenario: Detach succeeds

- **WHEN** `detach --output=json` succeeds
- **THEN** stdout includes `op: "detach"`, `ok: true`, and `port_id`

#### Scenario: Bind or unbind succeeds

- **WHEN** `bind` or `unbind` succeeds in JSON mode
- **THEN** stdout includes the matching `op`, `ok: true`, and `busid`

### Requirement: Watch event records are discriminated by kind

Watch JSON lines SHALL include `schema`, `kind`, `at`, and the payload appropriate for the event kind.

#### Scenario: Port reconnect exhaustion is rendered

- **WHEN** a `port_reconnect_exhausted` event is rendered
- **THEN** the record includes `port`, `attempts`, and `last_error`

#### Scenario: Unknown event concrete type is rendered

- **WHEN** an event kind/concrete type combination is not recognized by the renderer
- **THEN** the renderer returns an error instead of emitting an invalid v1 record

### Requirement: Status socket document is schema v1

The status UDS `GET /` response SHALL include schema, version, commit, uptime, listening state, bound devices, optional bound-device error, sessions, and kernel module state.

#### Scenario: Bound device rendering succeeds

- **WHEN** bound devices are serialized in status output
- **THEN** each row includes `busid`, `vid`, and `pid`
- **AND** `vid` and `pid` are lowercase four-digit hexadecimal strings prefixed with `0x`

#### Scenario: Bound device listing fails

- **WHEN** the daemon cannot list bound devices
- **THEN** `bound_devices_error` is included with diagnostic text
- **AND** the JSON schema remains otherwise valid

### Requirement: Kernel module state is tri-state JSON

Kernel module status JSON SHALL render each module as `"loaded"`, `"missing"`, or `"unknown"`.

#### Scenario: Module probe is blocked

- **WHEN** probing a module returns an error other than not-exist
- **THEN** the module state is `"unknown"` instead of `"missing"`

#### Scenario: Non-Linux status is rendered

- **WHEN** module probing runs on a non-Linux build
- **THEN** each canonical module key reports `"unknown"`

### Requirement: JSON v1 is forward-compatible

JSON consumers SHALL ignore unknown additive fields, while producers SHALL bump the schema envelope for breaking field-name, type, or semantic changes.

#### Scenario: Additive field appears

- **WHEN** a future v1 output includes a new field
- **THEN** old consumers remain valid if they ignore the field
