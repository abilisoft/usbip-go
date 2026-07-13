## MODIFIED Requirements

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
