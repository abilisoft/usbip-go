## MODIFIED Requirements

### Requirement: RemoteEndpoint values normalize USB/IP peers

RemoteEndpoint SHALL represent an exporter peer as `host:port`, default the port to 3240 only when it is omitted, accept valid scoped IPv6 literals, reserve bracket syntax for IPv6, reject explicit empty ports, and enforce ASCII DNS label and aggregate hostname bounds.

#### Scenario: Port is omitted

- **WHEN** an operator or library caller supplies a remote host without a port delimiter
- **THEN** the parsed RemoteEndpoint uses TCP port 3240

#### Scenario: Port delimiter is present without a value

- **WHEN** an endpoint ends in an explicit port delimiter such as `host:` or `[::1]:`
- **THEN** parsing fails instead of treating the port as omitted

#### Scenario: Scoped IPv6 is supplied

- **WHEN** an endpoint contains a valid scoped IPv6 literal such as `fe80::1%eth0` or `[fe80::1%eth0]:3240`
- **THEN** parsing preserves the zone and succeeds

#### Scenario: Brackets contain a non-IPv6 host

- **WHEN** bracket syntax contains an IPv4 literal or hostname
- **THEN** parsing fails because brackets are reserved for IPv6 literals

#### Scenario: DNS hostname exceeds aggregate bound

- **WHEN** the hostname excluding one optional final root dot exceeds 253 bytes
- **THEN** parsing fails even when every individual label is at most 63 bytes

#### Scenario: Endpoint is emitted

- **WHEN** a RemoteEndpoint is rendered in JSON or logs
- **THEN** the rendered value is stable `host:port` text with IPv6 literals bracketed
