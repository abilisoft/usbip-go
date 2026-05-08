# usbip-go

A pure-Go library and CLI for the USB/IP protocol. It lets a Linux host share
USB devices over TCP to any USB/IP-compatible importer, and lets an importer
consume those devices as if they were locally attached.

## Operator vocabulary

One binary — `usbip` — handles all roles via subcommands:

| Subcommand group | Role |
|---|---|
| `usbip serve` | Runs the Exporter daemon |
| `usbip attach`, `usbip detach`, `usbip list` | Importer client operations |

Kernel module names (`vhci_hcd`, `usbip_host`) are fixed external vocabulary;
this project does not try to rename them in prose.

## Language

### Roles

**Exporter**:
A host that makes one or more local USB devices available over the network.
Runs the `usbip_host` kernel module; binds devices and listens on TCP port 3240.
_Avoid_: server, host, publisher, provider.

**Importer**:
A host that attaches a remote USB device to its own USB bus via a virtual host
controller. Runs the `vhci_hcd` kernel module; dials the exporter and receives
a kernel-owned socket after the handshake.
_Avoid_: client, consumer, receiver.

### Devices and identity

**Device**:
A single USB device, either local (discovered from sysfs) or remote (decoded
from a devlist reply). Carries class, speed, vendor/product IDs, and interface
descriptors. A Device is always scoped to a host — two hosts with the same
physical device have two distinct Device values.
_Avoid_: USB device, peripheral, gadget.

**BusID**:
The stable Linux USB topology identifier that names a Device on its host
(e.g. `1-1.2`). Encodes the bus number and port path; survives replug to the
same port but changes when the device moves to a different port.
_Avoid_: device ID, path, address, node.

**RemoteEndpoint**:
A `host:port` pair that identifies an Exporter peer. Port defaults to 3240
when omitted. Carries no per-Device identity — one RemoteEndpoint may expose
many Devices.
_Avoid_: address, server address, peer.

### Kernel handoff

**Bind**:
The act of detaching a Device from its native driver and claiming it under the
`usbip_host` kernel module so it can be exported. Bind is reversible (Unbind
restores the original driver). A bound Device is **exportable**.
_Avoid_: expose, share, export (verb — Export is a whole role, Bind is the
per-device operation within it).

**Handshake**:
The OP_REQ_DEVLIST / OP_REP_DEVLIST / OP_REQ_IMPORT / OP_REP_IMPORT exchange
that happens entirely in Go before the kernel takes ownership of the TCP socket.
The Handshake negotiates which Device to attach and hands the socket fd to the
kernel via sysfs. After Handshake completes, Go never reads or writes URB frames.
_Avoid_: negotiation, setup, connection establishment.

**URB traffic**:
The USB Request Block frames that flow after the Handshake is complete. Owned
entirely by the kernel on both sides — Go never touches these frames.
_Avoid_: USB frames, post-handshake traffic.

### Lifecycle concepts

**Port**:
A vhci virtual host controller slot on the importer. Each successful Attach
occupies one Port. A Port has a numeric ID, a Status (available / used /
error), and a Speed. Many Ports can exist simultaneously.
_Avoid_: slot, channel, vhci port (too impl-specific in prose).

**Attachment**:
The state in which a remote Device is bound to a local Port and the kernel is
driving URB traffic. An Attachment exists from the moment the importer's kernel
receives the socket fd until Detach or connection drop.
_Avoid_: connection, link, session (Session belongs to the exporter side).

**Session**:
A single active connection on the **exporter** side, identified by a UUIDv7
SessionID. Created when the Handshake completes; destroyed when the socket
closes or Disconnect is called. Tracks bytes in/out and the peer address.
_Avoid_: connection, stream, client connection.

**SessionID**:
A UUIDv7 value that uniquely identifies one Session. UUIDv7 encodes a
millisecond-resolution timestamp, so sessions sort chronologically without a
separate `started_at` index.
_Avoid_: connection ID, session token, handle.

### Operational concepts

**Reconnect watcher**:
A background goroutine (one per attached Device) that re-runs the Handshake
when the connection drops. Uses per-Port generation tokens to detect stale
attempts. Backs off exponentially between attempts.
_Avoid_: retry loop, auto-reconnect.

**Drain**:
A graceful shutdown command that instructs a running Exporter to refuse new
Handshakes, wait for in-flight Sessions to close (up to a timeout), then exit.
Enables zero-downtime upgrades when paired with systemd socket activation.
_Avoid_: graceful shutdown (too generic), stop.

**Event**:
A domain notification emitted when something meaningful changes: Port state
(attached / detached / errored / reconnect exhausted), Device bind state
(bound / unbound), or Session lifecycle (started / ended). Eight concrete
event kinds. Consumers subscribe via `Importer.Watch` (port + device events,
both kernel-sourced and app-synthesized) or `Exporter.WatchSessions` (session
lifecycle, app-synthesized).

The split between `Watch` and `WatchSessions` is by domain concern, not by
event source. Port lifecycle is one bounded-context concern: `PortDetached`
(kernel uevent) and `PortReconnectExhausted` (app reconnect watcher) both
answer "what happened to this port?" — so they share one stream. Sessions
are a separate exporter-side accounting concern (UUIDv7-keyed, tracks
bytes), so they get their own stream.
_Avoid_: message, notification, update.

### Protocol layer

**Wire**:
The binary encoding of the USB/IP handshake protocol (version 0x0111).
Covers OP_REQ_DEVLIST, OP_REP_DEVLIST, OP_REQ_IMPORT, OP_REP_IMPORT, and
the 312-byte device descriptor. Everything is big-endian. URB traffic is
outside Wire scope.
_Avoid_: protocol, on-wire format, codec (Codec is the Go type that
implements Wire encode/decode).

## Relationships

- An **Exporter** exposes zero or more **Devices** via **Bind**
- An **Importer** **Attaches** one **Device** per **Port**; each Attach creates one **Session** on the exporter
- A **Session** lives until the importer **Detaches** or the TCP connection drops
- A **Reconnect watcher** re-creates an **Attachment** by running a new **Handshake**, producing a new **Session** with a fresh **SessionID**
- A **Drain** ends all **Sessions** and stops the **Exporter**

## Example dialogue

> **Dev:** "When the reconnect watcher kicks in, does it reuse the old Session?"
>
> **Domain expert:** "No — each Handshake produces a brand new Session with a new
> SessionID. From the importer's perspective the Port ID is the same, but from the
> exporter's accounting the previous Session ended and a new one started. That's why
> byte counters reset."
>
> **Dev:** "And if two importers try to Attach the same BusID at once?"
>
> **Domain expert:** "The second one gets ErrDeviceAlreadyBound — the Device is in
> SDEV_ST_USED state on the exporter side. The first importer's Handshake won the
> race; the second one has to poll or watch for the Session to end."

## Flagged ambiguities

- **"connection"** — used loosely in conversation to mean the TCP socket, the
  Handshake, the Session, and the Attachment. Resolved: use **Session** for the
  exporter's named, accounted unit; **Attachment** for the importer's kernel state;
  **Handshake** for the setup exchange.
- **"export" (verb)** — sometimes means the whole Exporter role, sometimes just
  Bind. Resolved: **Bind** is the per-device verb; **Exporter** is the role.
- **"attach" / "Attachment"** — `Attach` is the operation; `Attachment` is the
  resulting state. Both are valid; don't use "attach" as a noun.
- **"client / server"** — abandoned in favour of **Importer / Exporter** everywhere.
  USB/IP roles flip depending on perspective (importer is the TCP client but the USB
  consumer). The kernel terms are canonical.
