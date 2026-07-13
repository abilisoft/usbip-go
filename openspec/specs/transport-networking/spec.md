## Purpose

Specify TCP transport behavior, socket tuning, deadline handling, listener lifecycle, and transport-option validation.

## Requirements

### Requirement: Transport abstracts TCP dial and listen behavior

The app layer SHALL depend on a Transport interface with Dial and Listen methods, while production uses `internal/adapter/transport.NetTransport`.

#### Scenario: Tests need network seams

- **WHEN** app or facade tests need deterministic transport behavior
- **THEN** they can inject a fake Transport without using real TCP sockets

### Requirement: Dial normalizes default USB/IP port

Outbound dials SHALL normalize RemoteEndpoint port zero to the USB/IP default port 3240 before calling the network stack.

#### Scenario: Remote omits port

- **WHEN** a caller dials a RemoteEndpoint with no explicit port
- **THEN** the TCP address uses port 3240

### Requirement: Dial enables TCP_NODELAY

Outbound TCP connections SHALL enable `TCP_NODELAY` to avoid Nagle-delaying small handshake frames.

#### Scenario: TCP_NODELAY fails fatally

- **WHEN** setting `TCP_NODELAY` returns a fatal socket state error such as closed, not connected, or bad fd
- **THEN** the dialed connection is closed and Dial returns an error

#### Scenario: TCP_NODELAY is rejected non-fatally

- **WHEN** setting `TCP_NODELAY` fails but the connection remains usable
- **THEN** the transport logs a warning and returns the connection

### Requirement: TransportOptions zero value preserves defaults

TransportOptions SHALL treat zero-valued fields as "inherit Go/kernel defaults" and SHALL validate negative fields before use.

#### Scenario: Negative option is supplied

- **WHEN** any duration, probe count, or buffer size in TransportOptions is negative
- **THEN** Importer or Exporter construction fails before network I/O begins

#### Scenario: No option is supplied

- **WHEN** TransportOptions is the zero value
- **THEN** existing TCP behavior is preserved

### Requirement: DialConnectTimeout caps outbound connect only

`DialConnectTimeout` SHALL apply to the TCP connect phase by using a per-call copy of the dialer.

#### Scenario: Concurrent dials use different options

- **WHEN** multiple goroutines dial with different connect timeouts
- **THEN** their per-call dialer copies do not race on shared timeout state

### Requirement: Socket buffers are tunable

`SendBufferBytes` and `ReceiveBufferBytes` SHALL request `SO_SNDBUF` and `SO_RCVBUF` on outbound dials and accepted listener connections.

#### Scenario: Linux doubles requested buffer

- **WHEN** tests inspect actual buffer sizes on Linux
- **THEN** they allow the kernel's doubled internal value to be greater than or equal to the requested value

### Requirement: TCP keepalive is tunable

Transport tuning SHALL use Go's keepalive configuration to apply idle, interval, and probe count when any keepalive field is non-zero.

#### Scenario: Keepalive field is set

- **WHEN** at least one keepalive TransportOptions field is non-zero
- **THEN** SO_KEEPALIVE is enabled and the non-zero parameters are forwarded to the TCP connection

### Requirement: WAN and high-latency links are supported through explicit transport tuning

TransportOptions SHALL expose enough TCP controls for library callers to tune USB/IP handshakes for high-latency, lossy, or bandwidth-delay-product-sensitive links without changing the USB/IP wire format.

#### Scenario: Caller tunes for a high-latency path

- **WHEN** a caller supplies connect timeout, keepalive, socket-buffer, and handshake-deadline TransportOptions
- **THEN** outbound importer dials and exporter-owned accepted connections receive those options before the USB/IP handshake begins

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

### Requirement: Listen returns a context-bound listener

Transport Listen SHALL bind a TCP listener that closes automatically when the supplied context is cancelled and also supports explicit idempotent Close.

#### Scenario: Listen context is already cancelled

- **WHEN** Listen is called with a cancelled context
- **THEN** it returns an error before binding

#### Scenario: Caller closes listener

- **WHEN** Close is called on the returned listener
- **THEN** the listener closes once and waits for its context watcher goroutine to exit

### Requirement: Accepted connections inherit listener tuning

When Listen receives non-zero TransportOptions, each accepted TCP connection SHALL be tuned before being returned from Accept.

#### Scenario: Accept returns non-TCP connection in tests

- **WHEN** the accepted connection is not a `*net.TCPConn`
- **THEN** tuning is skipped and the connection is returned

#### Scenario: Accept-time tuning hits fatal socket error

- **WHEN** a fatal socket option error occurs during accept-time tuning
- **THEN** the connection is closed and Accept returns an error

### Requirement: Facade ListenAndServe honors exporter transport options

`Exporter.ListenAndServe` SHALL bind through the Transport adapter so accepted connections inherit `WithExporterTransportOptions`.

#### Scenario: Listen fails

- **WHEN** Transport.Listen returns an error
- **THEN** ListenAndServe returns that error and does not invoke Serve

#### Scenario: Serve returns early

- **WHEN** ListenAndServe's Serve call returns before context cancellation
- **THEN** ListenAndServe closes the listener it created

### Requirement: ListenAndServe reserves lifecycle before transport bind

The public `Exporter.ListenAndServe` path SHALL acquire the authoritative
internal Serve reservation before invoking `Transport.Listen`, and the listener
setup SHALL receive a context that Exporter Shutdown can cancel.

#### Scenario: Exporter is already shut down

- **WHEN** ListenAndServe is invoked after Shutdown
- **THEN** it returns the terminal Exporter sentinel
- **AND** `Transport.Listen` is not called

#### Scenario: Another Serve call is active

- **WHEN** ListenAndServe is invoked while another Serve call owns the reservation
- **THEN** it returns the overlapping-Serve sentinel
- **AND** `Transport.Listen` is not called

#### Scenario: Shutdown begins during transport bind

- **WHEN** `Transport.Listen` is waiting on the supplied context and Shutdown begins
- **THEN** that context is cancelled
- **AND** no listener remains installed after the Serve call exits

#### Scenario: Transport bind succeeds

- **WHEN** the lifecycle reservation succeeds and `Transport.Listen` returns a listener
- **THEN** the listener is installed for Shutdown and accept-loop ownership
- **AND** it is closed before ListenAndServe returns
