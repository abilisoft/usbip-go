## MODIFIED Requirements

### Requirement: WAN and high-latency links are supported through explicit transport tuning
TransportOptions SHALL expose enough TCP controls for library callers to tune USB/IP handshakes for high-latency, lossy, or bandwidth-delay-product-sensitive links without changing the USB/IP wire format. Focused single-kernel integration SHALL prove the complete USB/IP TCP stream remains correct through deterministic 20 ms-per-chunk bidirectional proxy delay. Tracked two-guest integration SHALL additionally prove production USB/IP remains correct when exactly 25 ms of `tc netem` delay is active on both dedicated guest egress paths.

#### Scenario: Caller tunes for a high-latency path

- **WHEN** a caller supplies connect timeout, keepalive, socket-buffer, and handshake-deadline TransportOptions
- **THEN** outbound importer dials and exporter-owned accepted connections receive those options before the USB/IP handshake begins

#### Scenario: Single-kernel data path crosses fixed bidirectional proxy delay

- **WHEN** a live-kernel USB/IP connection routes its complete TCP stream through a forwarder that delays every forwarded chunk by exactly 20 ms in both directions and configured handshake deadlines exceed the injected delay budget
- **THEN** the import handshake completes through that forwarder
- **AND** the same forwarder records delayed post-handoff URB traffic in both directions
- **AND** a deterministic payload arrives byte-for-byte

#### Scenario: Two-guest production data path crosses fixed bidirectional network delay

- **WHEN** distinct exporter and importer guest kernels communicate through the production USB/IP path with exactly 25 ms of `tc netem` delay installed on each guest's dedicated inter-guest egress interface
- **THEN** both guests report the configured qdisc and its directional byte and packet counters advance during exactly three attachment cycles
- **AND** every import handshake and exact-Port Detach succeeds through the delayed inter-guest path
- **AND** unique deterministic ACM payloads arrive byte-for-byte in both directions during every cycle
- **AND** the fixed delay is not reported as evidence for loss, jitter, reordering, bandwidth limiting, outage, or reconnect behavior
