# Drain mechanism: HTTP-over-UDS with polling completion

The `usbip drain` operator command talks to a running `usbip serve`
daemon over a Unix-domain socket carrying HTTP. The client posts to
`/drain`, the server cancels its accept loop, and the client polls
`GET /` until the `sessions` array drains to empty (or its own
`--drain-timeout` expires). The server's authoritative bound on
in-flight session drain is `--shutdown-timeout`; the client's
`--drain-timeout` is a UI bound on how long the operator waits for
the polling loop to settle.

## Considered alternatives

- **SIGUSR1 / signal-based control** (nginx, NATS, postgres convention).
  Rejected: signals are one-bit; the client cannot acknowledge or
  receive a typed completion signal, the server cannot return an
  error, and parameters (e.g. drain timeout) cannot be passed.
  Permission model collapses to uid-match or root, losing the
  operator-group control that UDS file-mode provides.
- **systemd `Type=notify` socket**. Rejected: this is the wrong
  direction (daemon-to-supervisor state reporting, not
  operator-to-daemon command). Non-systemd hosts (Docker,
  containerd) lose the drain command entirely.
- **gRPC unix-socket admin server**. Rejected: ~30 transitive
  packages for a 3-RPC control plane is overkill. The decisive
  factor against gRPC is the same one that drove the metrics gut
  in ADR-0010 — dependency footprint imposed on every library
  consumer.
- **Custom binary protocol over UDS**. Rejected: forces this project
  to invent length framing, version negotiation, error codes, and
  compatibility rules that HTTP already provides. Less inspectable —
  operators cannot `curl` the socket for emergency diagnosis.

## Why HTTP-over-UDS

The same socket is the multi-purpose admin channel: `GET /` returns
the status JSON document (version, commit, listening, kernel
modules, sessions); `POST /drain` triggers graceful shutdown; future
operations (`POST /reload-config`, `POST /sessions/{id}/revoke`,
`POST /debug/log-level`) drop in alongside without protocol-version
bumps. JSON evolution adds fields to the status document without
breaking partial decoders, and HTTP method/path/status-code
semantics carry per-operation success/error without layering work.

Operator inspectability is a first-class win: anyone with the
`usbip-go` group can `curl --unix-socket /run/usbip-go/status.sock
http://x/` to read state during an incident, without needing the
`usbip` binary itself. The same path supports k8s and systemd-style
readiness probes through the standard `curl --unix-socket` idiom.

The current implementation lives in `cmd/usbip/status.go` (HTTP
server + bind / chmod / chown / unlink-stale dance),
`cmd/usbip/status_exporter.go` (status snapshot + drain registration),
and `cmd/usbip/drain.go` (client subcommand).

## Polling vs server-push

The client polls `GET /` every 500ms until `sessions == []`. SSE or a
streaming `/drain/wait` would eliminate the round-trip waste, but
adds connection state for marginal benefit on an operation whose
total wall-time is bounded by `--shutdown-timeout` (default 30s).
Polling stays within ~60 round-trips per drain, all over a local
UDS — no measurable cost. Push is a future option if a real
operator pain point appears.

## Two-timeout policy

Two timeouts coexist:

- **Server `--shutdown-timeout`** (default 30s). Bounds the actual
  in-flight session drain. AUTHORITATIVE: the daemon refuses to wait
  beyond this even if more sessions are still active.
- **Client `--drain-timeout`** (default 60s). Bounds how long
  `usbip drain` waits for the polling loop to detect idle. UI-only;
  the daemon ignores it.

When the two disagree:

- `--drain-timeout < --shutdown-timeout`: client gives up first,
  reports timeout exit code, daemon keeps draining and exits when
  its own timeout fires. Operator sees a misleading "drain timed
  out" but the daemon completes correctly. Mitigation: operator
  bumps `--drain-timeout` or aligns with server config.
- `--drain-timeout > --shutdown-timeout`: server hits its cap first,
  force-closes lingering sessions, exits. Client's next poll sees
  ECONNREFUSED, treats as drain success. This is the well-behaved
  case.

The recommended default is `--drain-timeout > --shutdown-timeout`
so the operator-visible result reflects the daemon's actual
behavior.

## Idempotency

`POST /drain` is idempotent at the application level:
`Exporter.Shutdown` is guarded by `sync.Once` so multiple
cancellations fold to one. The HTTP handler also guards against
spawning redundant cancellation goroutines and emitting duplicate
log lines on repeated calls — this is the one concrete addition
this ADR introduces over the prior implementation.

## Permissions

UDS file mode `0660`, owned by the configured
`--status-socket-group` (default `usbip-go`). Operators added to
that group can drain the daemon without root. The status endpoint
exposes only the daemon's own state — no kernel writes flow through
the UDS.
