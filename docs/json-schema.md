# JSON schema v1 reference

Stable contract for every JSON object emitted by usbip-go. This
document fixes field names, types, and semantics at schema v1 per
`openspec/specs/operations-observability/spec.md` and `openspec/specs/cli-interface/spec.md`.

The schema is mutable BEFORE the first stable release (v1.0.0).
Field semantics may shift between v0.x and v1.0.x without a schema
bump because no public consumer has shipped against them. Once
v1.0.0 lands, any breaking change here requires a documented
schema bump and a GitHub Release note at tag time.

The CLI's `--output=table` (default) human-readable format is NOT
covered by this contract: borders, spacing, and column
character-set may change between releases (e.g. ASCII vs Unicode
box-drawing) without a major-version bump. Scripts and parsers
MUST use `--output=json` against this schema instead.

## Stability rules

- Every top-level JSON object has `"schema": "v1"` as its first
  field. Parsers MUST check it and either accept the known version
  or fail loudly.
- Watch-mode jsonlines records carry `"schema"` and `"kind"` on
  every line.
- v1 is **additively stable** within a major version: new fields
  may appear; consumers MUST ignore unknown fields. Existing field
  names, types, and semantics never change within v1.
- Any breaking change bumps the envelope to `"schema": "v2"` AND
  requires a major-version bump of the library. Both versions are
  emitted side-by-side via `--output=json-v1` / `--output=json-v2`
  for at least one minor release.
- Omitted or unknown `"schema"` is not v1 and is not supported.

Schema version is distinct from library version and from the
on-wire USB/IP protocol version (`0x0111`, see
[`protocol.md`](protocol.md)).

## Common view types

These embedded shapes appear inside every surface that lists
devices, ports, or sessions. They are defined once here and
referenced by every envelope below.

### `deviceView`

| Field | Type | Source | Notes |
|---|---|---|---|
| `busid` | string | `domain.BusID` | Linux USB topology identifier, e.g. `"1-1.2"`. |
| `busnum` | integer (u16) | `domain.Device.BusNum` | |
| `devnum` | integer (u16) | `domain.Device.DevNum` | |
| `speed` | string | `domain.Speed.String()` | Long-form: `unknown`, `Low-Speed (1.5Mbps)`, `Full-Speed (12Mbps)`, `High-Speed (480Mbps)`, `Wireless`, `SuperSpeed (5Gbps)`, `SuperSpeed+ (10/20Gbps)`, or `speed(N)` for unknown wire values. |
| `vendor_id` | string | `domain.Device.VendorID` | Lowercase 4-digit hex, e.g. `"0951"` — matches `lsusb` convention. |
| `product_id` | string | `domain.Device.ProductID` | Same format as `vendor_id`. |

### `portView`

| Field | Type | Source | Notes |
|---|---|---|---|
| `id` | integer (u32) | `domain.Port.ID` | Numeric vhci port id. |
| `status` | string | `domain.Status.String()` | `null`, `not-assigned`, `available`, `used`, `error`, or `status(N)` for unknown wire values. |
| `speed` | string | `domain.Port.Speed.String()` | Same enum as `deviceView.speed`. |
| `remote` | string | `domain.Port.Remote.String()` | `host:port`, IPv6 literals bracketed. |
| `busid` | string | `domain.Port.BusID` | Remote busid as reported by the exporter. |
| `local_busid` | string | `domain.Port.LocalBusID` | Local sysfs busid if the kernel mapped one; empty otherwise. |

### `sessionView`

| Field | Type | Source | Notes |
|---|---|---|---|
| `id` | string | `domain.SessionID.String()` | UUIDv7 canonical 36-character form. |
| `remote` | string | `domain.Session.RemoteAddr` | `host:port` of the accepted peer. |
| `busid` | string | `domain.Session.BusID` | Exported device's busid for this session. |
| `started_at` | string | RFC 3339 nano UTC | Wall-clock time of handshake completion. |
| `bytes_in` | integer (u64) | counter | Bytes received from the peer. |
| `bytes_out` | integer (u64) | counter | Bytes transmitted to the peer. |

## List envelopes (`usbip-go` CLI)

### `list --output=json`

Returned by `usbip-go list --local`, `usbip-go list --remote HOST`, and
equivalents.

```json
{
  "schema": "v1",
  "devices": [ deviceView, ... ]
}
```

### `port --output=json`

Returned by `usbip-go port --output=json`.

```json
{
  "schema": "v1",
  "ports": [ portView, ... ]
}
```

### Sessions list (status socket path)

Returned by the daemon status UDS — `GET /` over the path configured
with `--status-socket` (the CLI does not have a `status` subcommand).

```json
{
  "schema": "v1",
  "sessions": [ sessionView, ... ]
}
```

## Acknowledgement envelopes (mutating commands)

Every mutating subcommand emits a small ack envelope when
`--output=json`. The shared prefix is:

```json
{
  "schema": "v1",
  "op": "<op>",
  "ok": true,
  ...
}
```

`"ok"` is always `true` on success. Failures surface as a non-zero
exit code and a message on stderr, never as `{"ok": false}`.

### `attach`

```json
{
  "schema": "v1",
  "op": "attach",
  "ok": true,
  "port": portView
}
```

### `detach`

```json
{
  "schema": "v1",
  "op": "detach",
  "ok": true,
  "port_id": <u64>
}
```

### `bind`

```json
{
  "schema": "v1",
  "op": "bind",
  "ok": true,
  "busid": "<BusID>"
}
```

### `unbind`

```json
{
  "schema": "v1",
  "op": "unbind",
  "ok": true,
  "busid": "<BusID>"
}
```

## Watch events (jsonlines)

`usbip-go watch --output=json` emits one record per line. Every record
carries the schema envelope plus a `kind` discriminator. `kind`
comes from a closed set matching `pkg/domain.EventKind.String()`:

- `port_attached`
- `port_detached`
- `port_errored`
- `port_reconnect_exhausted`
- `device_bound`
- `device_unbound`
- `session_started`
- `session_ended`

### Common base (`eventBase`)

Every record has these three fields as its leading keys:

| Field | Type | Notes |
|---|---|---|
| `schema` | string | `"v1"`. |
| `kind` | string | Discriminator. See list above. |
| `at` | string | RFC 3339 nano UTC timestamp of the event. |

### `port_attached`

```json
{ "schema":"v1", "kind":"port_attached", "at":"...", "port": portView }
```

### `port_detached`

```json
{ "schema":"v1", "kind":"port_detached", "at":"...", "port": portView, "reason":"..." }
```

### `port_errored`

```json
{ "schema":"v1", "kind":"port_errored", "at":"...", "port": portView, "err":"..." }
```

### `port_reconnect_exhausted`

Emitted by `Importer.Watch` when the reconnect watcher gives up after
`AttachOptions.MaxAttempts` failed reattaches. `port` is a snapshot of
the last successful Attach (the kernel slot is gone at emission time);
`attempts` is the number of reconnect attempts actually made (NOT
`MaxAttempts`); `last_error` is the stringified final error. See
`openspec/specs/domain-model/spec.md`.

```json
{
  "schema": "v1",
  "kind": "port_reconnect_exhausted",
  "at": "...",
  "port": portView,
  "attempts": 3,
  "last_error": "..."
}
```

### `device_bound`

```json
{ "schema":"v1", "kind":"device_bound", "at":"...", "device": deviceView }
```

### `device_unbound`

```json
{ "schema":"v1", "kind":"device_unbound", "at":"...", "device": deviceView }
```

### `session_started`

```json
{ "schema":"v1", "kind":"session_started", "at":"...", "session": sessionView }
```

### `session_ended`

```json
{ "schema":"v1", "kind":"session_ended", "at":"...", "session": sessionView, "reason":"..." }
```

## Daemon status socket document

Served on `GET /` via the UDS configured with `--status-socket`.
Shape is `statusResponse` in
[`cmd/usbip-go/status.go`](../cmd/usbip-go/status.go).

```json
{
  "schema": "v1",
  "version": "X.Y.Z",
  "commit": "<short sha>",
  "uptime_sec": <i64>,
  "listening": {
    "addr": "0.0.0.0:3240",
    "activation": true,
    "accepting": true
  },
  "bound_devices": [
    { "busid": "1-1.2", "vid": "0x0951", "pid": "0x1666" },
    ...
  ],
  "bound_devices_error": "optional diagnostic text when bound-device listing fails",
  "sessions": [ sessionJSON, ... ],
  "kernel_modules": {
    "usbip_core": "loaded",
    "vhci_hcd":   "loaded",
    "usbip_host": "loaded",
    "usbip_vudc": "missing"
  }
}
```

`listening.activation` distinguishes socket-activated startup from
a plain `--listen` bind — operators can verify deploy-time
expectations without reading logs.

`listening.accepting` flips to `true` the moment the daemon's
accept loop enters its first `Accept` call (before any client
connects), and back to `false` when `Serve` returns. This lets
`/readyz` distinguish a listener that is merely bound from one
that has actually armed the accept loop.

`bound_devices_error` is omitted on the happy path. When the daemon
cannot list bound devices, `bound_devices` remains an empty array and
`bound_devices_error` carries a human-readable diagnostic so operators
can distinguish "nothing is exported" from "status collection failed".

`kernel_modules` values are one of `"loaded"`, `"missing"`,
`"unknown"`. The `/readyz` endpoint also consumes them to gate
readiness on `usbip_core` and `usbip_host` being loaded.

`sessions` entries use the same `sessionView` shape as the CLI
list surfaces, with two JSON field name differences that are
pinned by `openspec/specs/operations-observability/spec.md`:

- Status socket `sessionJSON.id` is the canonical UUIDv7 form.
- `started_at` is RFC 3339 nano UTC.

## Observability via slog

Per `openspec/specs/operations-observability/spec.md`, this project
ships no Prometheus metrics. Every operation that crosses a state
boundary emits a structured slog record carrying an `outcome` field
with a closed-set classification.
Operators query journald (`journalctl --output=json`) instead of
scraping `/metrics`.

The closed outcome enumerations:

| Operation | Outcome values |
|---|---|
| `exporter_bind` | `ok`, `already_bound`, `not_found`, `permission`, `error` |
| `exporter_unbind` | `ok`, `not_bound`, `permission`, `error` |
| `exporter_session_handshake` | `handshake_ok`, `rejected_acl`, `rejected_rate`, `rejected_cap`, `handshake_failed` |
| `exporter_disconnect_reason` | `client_gone`, `kernel_error`, `shutdown`, `protocol_error` |
| `importer_attach` | `ok`, `permission`, `no_free_port`, `protocol_mismatch`, `dial_failed`, `kernel_error` |
| `importer_detach` | `ok`, `not_found`, `error` |
| `importer_reconnect` | `ok`, `backoff`, `exhausted`, `canceled` |

Per-session byte counters are explicitly omitted from v1. The kernel
owns the URB path; polling byte counters at our layer would produce
approximate, misleading numbers. Operators who need byte-level
visibility read kernel socket stats via `ss -tn`.

Build provenance (`version`, `commit`, `build_date`, `go_version`)
appears as fields on the `"usbip-go serve starting"` slog record at
daemon startup. Operators query for it via `journalctl --output=json |
jq 'select(.MESSAGE == "usbip-go serve starting")'`.

## Forward compatibility

All consumers MUST treat unknown fields as opaque and pass them
through or ignore them silently. The `output_forward_compat_test.go`
suite in `cmd/usbip-go` asserts that renderers emit fields not present
in older fixtures without breaking older parsers that ignore them.

When a breaking change is required, the library bumps the schema
envelope from `"v1"` to `"v2"` AND the library major version. The
old and new forms coexist behind `--output=json-v1` and
`--output=json-v2` for at least one minor release before the old
form is retired.
