## Purpose

Specify the USB/IP handshake wire behavior implemented by `internal/adapter/wire` and consumed by importer/exporter application services.

## Requirements

### Requirement: Wire scope is limited to the USB/IP handshake

The Go wire codec SHALL encode and decode only `OP_REQ_DEVLIST`, `OP_REP_DEVLIST`, `OP_REQ_IMPORT`, `OP_REP_IMPORT`, headers, device descriptors, and interface descriptors.

#### Scenario: URB traffic begins

- **WHEN** the socket fd has been handed to the kernel after a successful import
- **THEN** Go stops reading and writing URB frames
- **AND** URB traffic remains kernel-owned

### Requirement: OP header is 8 bytes and big-endian

Every request and reply SHALL begin with a common 8-byte header containing version `0x0111`, opcode, and status in network byte order.

#### Scenario: Version mismatch is decoded

- **WHEN** an inbound header does not carry version `0x0111`
- **THEN** the decoder returns `ErrProtocolMismatch` with observed/wanted context

#### Scenario: Reply status is non-zero

- **WHEN** a non-import reply carries non-zero status
- **THEN** the decoder returns `ErrProtocolError`

### Requirement: Supported opcodes match upstream USB/IP

The codec SHALL support `OpReqDevlist` `0x8005`, `OpRepDevlist` `0x0005`, `OpReqImport` `0x8003`, and `OpRepImport` `0x0003`.

#### Scenario: Unknown opcode is decoded

- **WHEN** the header opcode is not in the supported set
- **THEN** the decoder classifies the frame as a protocol mismatch

### Requirement: Device descriptor layout is fixed

The codec SHALL encode and decode the 312-byte device descriptor with path, BusID, bus/dev numbers, speed, vendor/product IDs, BCD device, class/subclass/protocol, config value, configuration count, and interface count at documented offsets.

#### Scenario: Descriptor is encoded

- **WHEN** a Device is encoded into a USB/IP reply
- **THEN** all multi-byte integers use `binary.BigEndian`
- **AND** fixed-width strings are NUL-padded to their declared sizes

#### Scenario: Descriptor is decoded

- **WHEN** a descriptor lacks local-only metadata
- **THEN** missing local fields such as Manufacturer, Product, and interface Alt remain empty or zero

### Requirement: Interface descriptors are 4 bytes each

Each interface in a devlist reply SHALL be encoded as class, subclass, protocol, and reserved padding byte, with no alternate-setting on the wire.

#### Scenario: Interface is decoded from wire

- **WHEN** an interface descriptor is decoded
- **THEN** Interface.Alt is set to zero because the wire format does not carry alternate-setting

### Requirement: Empty devlist replies are valid

`OP_REP_DEVLIST` with `nDevices=0` SHALL decode successfully as an empty listing.

#### Scenario: Exporter has no exportable devices

- **WHEN** a peer requests the device list
- **THEN** the exporter may return a valid devlist reply with zero devices

### Requirement: Import error replies map to device availability sentinels

`OP_REP_IMPORT` with non-zero status SHALL be treated as the protocol's normal rejection channel for unavailable BusIDs.

#### Scenario: Import is rejected

- **WHEN** `OP_REP_IMPORT` contains a non-zero status and no body
- **THEN** importer callers receive the canonical device-not-found/busy/unavailable classification instead of a generic framing error

### Requirement: Decoder is defensive for malformed input

The codec SHALL preserve clean EOF, wrap short reads with context, reject invalid fixed-width strings, report already-buffered extra devlist bytes with a warning without reading beyond the complete count-delimited frame, and surface padded-string truncation as advisory decode flags.

#### Scenario: Frame is truncated

- **WHEN** a body ends mid-field
- **THEN** the decoder returns `io.ErrUnexpectedEOF` wrapped with field or phase context

#### Scenario: Devlist has already-buffered extra bytes

- **WHEN** all declared devices are decoded and the decoder already buffered bytes beyond the frame
- **THEN** the codec logs a warning and ignores the extra bytes

#### Scenario: Exact devlist frame remains open

- **WHEN** all declared devices are decoded from a stream that remains open with no buffered trailing data
- **THEN** the decoder returns immediately without probing the underlying stream for another byte

### Requirement: Wire compatibility is pinned by fixtures and fuzzing

The wire package SHALL maintain conformance fixtures from upstream captures and fuzz targets with valid full-frame seeds for protocol-critical decoders.

#### Scenario: Fixture is replayed

- **WHEN** conformance tests decode and re-encode real upstream captures
- **THEN** the bytes round-trip without drift

#### Scenario: Malformed input is fuzzed

- **WHEN** fuzz targets generate malformed USB/IP frames from seeds that include complete headers and bodies
- **THEN** each target reaches its intended decoder and returns classified errors without panics, hangs, or unbounded reads
