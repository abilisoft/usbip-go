# JSON schema v1 reference

Stable contract for every JSON object emitted by usbip-go. This
document fixes field names, types, and semantics at schema v1 per
spec §7.5. It is one of the committed artefacts gated by the
`changelog-check` release job — any change here requires a
documented schema bump.

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
| `speed` | string | `domain.Speed.String()` | One of `unknown`, `low`, `full`, `high`, `wireless`, `super`, `super+`. |
| `vendor_id` | string | `domain.Device.VendorID` | Lowercase 4-digit hex, e.g. `"0951"` — matches `lsusb` convention. |
| `product_id` | string | `domain.Device.ProductID` | Same format as `vendor_id`. |

### `portView`

| Field | Type | Source | Notes |
|---|---|---|---|
| `id` | integer (u32) | `domain.Port.ID` | Numeric vhci port id. |
| `status` | string | `domain.Status.String()` | `available`, `notassigned`, `used`, `error`, `suspended`, `unavailable`. |
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

Returned by the daemon status UDS and by
`usbip-go status --output=json`:

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
- `device_bound`
- `device_unbound`
- `remote_device_added`
- `remote_device_removed`
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

### `device_bound`

```json
{ "schema":"v1", "kind":"device_bound", "at":"...", "device": deviceView }
```

### `device_unbound`

```json
{ "schema":"v1", "kind":"device_unbound", "at":"...", "device": deviceView }
```

### `remote_device_added`

```json
{ "schema":"v1", "kind":"remote_device_added", "at":"...", "remote":"host:port", "device": deviceView }
```

### `remote_device_removed`

```json
{ "schema":"v1", "kind":"remote_device_removed", "at":"...", "remote":"host:port", "busid":"<BusID>" }
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
[`cmd/usbipd-go/status.go`](../cmd/usbipd-go/status.go).

```json
{
  "schema": "v1",
  "version": "vX.Y.Z",
  "commit": "<short sha>",
  "uptime_sec": <i64>,
  "listening": {
    "addr": "0.0.0.0:3240",
    "activation": true,
    "accepting": true
  },
  "bound_devices": [
    { "busid": "1-1.2", "vid": "0951", "pid": "1666" },
    ...
  ],
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

`listening.accepting` stays `false` until the first successful
`Accept` returns. This is intentional: it means `/readyz` can
distinguish a listener that is merely bound from one that has
actually armed the accept loop.

`kernel_modules` values are one of `"loaded"`, `"missing"`,
`"unknown"`. They map 1:1 to the `usbip_kernel_modules_loaded`
Prometheus gauge.

`sessions` entries use the same `sessionView` shape as the CLI
list surfaces, with two JSON field name differences that are
pinned by spec §7.7:

- Status socket `sessionJSON.id` is the canonical UUIDv7 form.
- `started_at` is RFC 3339 nano UTC.

## Metrics

The Prometheus `/metrics` endpoint is NOT JSON — it emits the
Prometheus text exposition format. The metric catalogue below lists
every stable v1 metric and matches spec §11.5.5 verbatim.

| Name | Type | Unit | Labels | Description |
|---|---|---|---|---|
| `usbip_exporter_sessions_active` | gauge | — | — | Current accepted sessions. |
| `usbip_exporter_sessions_accepted_total` | counter | — | `outcome`={`handshake_ok`,`rejected_acl`,`rejected_rate`,`rejected_cap`,`handshake_failed`} | Cumulative accept events. |
| `usbip_exporter_handshake_duration_seconds` | histogram | seconds | `op`={`devlist`,`import`} | Wall time per OP handshake. |
| `usbip_exporter_bind_total` | counter | — | `outcome`={`ok`,`already_bound`,`not_found`,`permission`,`error`} | Bind attempts. |
| `usbip_exporter_unbind_total` | counter | — | `outcome`={`ok`,`not_bound`,`permission`,`error`} | Unbind attempts. |
| `usbip_exporter_disconnect_total` | counter | — | `reason`={`graceful`,`client_gone`,`kernel_error`,`shutdown`} | Session end reasons. |
| `usbip_importer_attaches_total` | counter | — | `outcome`={`ok`,`permission`,`no_free_port`,`protocol_mismatch`,`dial_failed`,`kernel_error`} | Attach attempts. |
| `usbip_importer_detaches_total` | counter | — | `outcome`={`ok`,`not_found`,`error`} | Detach attempts. |
| `usbip_importer_ports_active` | gauge | — | — | Currently-attached vhci ports. |
| `usbip_importer_reconnect_attempts_total` | counter | — | `outcome`={`ok`,`backoff`,`exhausted`,`canceled`} | Reconnect attempts by auto-reconnect watcher. |
| `usbip_adapter_sysfs_write_failures_total` | counter | — | `path`, `errno` | Sysfs write errors. Cardinality bounded by known paths. |
| `usbip_kernel_modules_loaded` | gauge | — | `module`={`usbip_core`,`vhci_hcd`,`usbip_host`,`usbip_vudc`} | 1 if module loaded, 0 otherwise. |
| `usbip_build_info` | gauge | — | `version`, `commit`, `go_version` | Always 1; labels carry build metadata. |

Per-session byte counters are explicitly omitted from v1. The
kernel owns the URB path; polling byte counters at our layer would
produce approximate, misleading numbers. Operators who need
byte-level visibility read kernel socket stats via `ss -tn`.

Label values come from closed small sets. There are no `busid` or
`remote` labels — those would be unbounded and would overwhelm
Prometheus.

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
