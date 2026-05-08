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

`POST /drain` is idempotent at three layers:

1. **HTTP handler**: an `atomic.Bool` CAS gate ensures only the first
   POST that wins the swap spawns the drain goroutine. That POST
   returns `202 Accepted` (RFC 9110 §15.3.3 — request accepted for
   asynchronous processing). Subsequent POSTs see the gate already
   set and return `200 OK` as a no-op acknowledgement. The split
   lets monitoring tools distinguish "I initiated this drain" from
   "someone else already did" without parsing a response body.
2. **Run-side cancel**: the cancel func registered via
   `statusExporter.setDrain` is `cancelServe(errDrainRequested)`,
   which delegates to `context.WithCancelCause`. The underlying ctx
   cancellation is naturally idempotent — second cancels are no-ops.
3. **Exporter.Shutdown**: under the exporter mutex, the `shutdown`
   flag flips on first entry and the tracked listener is captured
   and cleared. Subsequent calls find an empty listener field, an
   empty sessions map, and proceed through the same cleanup code
   path with nothing to do — no panic, no duplicate session-end
   events, just a fast return.

The handler-level CAS is the layer this ADR introduces. The other
two layers were already idempotent through pre-existing design.

## What happens to active USB traffic during drain

Drain does NOT cut active URB traffic. The kernel owns the URB path
on both sides of the USB/IP link (see ADR-0001 + the `URB traffic`
glossary entry in CONTEXT.md). The daemon's job is:

1. Refuse new accepts (`accepting` flag flips false; the listener
   stays bound so `systemctl restart usbip` does not see
   `connect-refused`).
2. Wait for in-flight Sessions (the exporter-side accounting unit)
   to end naturally as the kernel signals `port_detached` or
   `device_unbound` events.
3. After `--shutdown-timeout` elapses, force-close any sessions
   still tracked by the daemon. The KERNEL still holds its dup of
   each session's fd, so URB traffic continues until the IMPORTER
   peer detaches its port — the daemon's exit only stops the daemon
   from accounting the session, not the data path.

Operators upgrading via drain-and-replace see in-flight transfers
continue across the daemon swap because the kernel state survives
`usbip` process exit and the new process picks up the live ports
through the kernel's existing accounting (per v1 contract §5.4
item 7).

## Failure modes and operator escape hatches

| Failure | Effect | Operator action |
|---|---|---|
| `src.Drain` returns non-nil (rare; means `Exporter.Shutdown` errored on a wedged session) | The drain goroutine logs at error level; the `drainStarted` gate stays set. Subsequent `POST /drain` returns `200 OK` no-op. The daemon STILL exits when its own ctx cancels (deferred `drainExporter` in `runDaemon` re-enters `Exporter.Shutdown`; the second call finds the listener already cleared and the sessions map drained, so it returns quickly). | Wait for the daemon to exit on its own; check journald for the error. If drain was caused by a pending upgrade, the new process binds the activated socket regardless. |
| Daemon ctx cancels mid-drain (operator Ctrl-C after triggering drain) | The drain goroutine receives the cancellation through the daemon ctx and aborts its wait. The deferred `drainExporter` in `runDaemon` runs the same `Exporter.Shutdown` sync.Once — second invocation returns immediately. | Intentional: operator-visible Ctrl-C wins; in-flight sessions force-close. |
| Drain hangs past `--shutdown-timeout` | `Exporter.Shutdown` returns; lingering sessions are force-closed; daemon exits. Client polling sees ECONNREFUSED on the next `GET /` and treats it as drain success. | None — this is the well-behaved path. |
| Kernel module hung (rare; sysfs writes stall) | Drain blocks inside the per-session `kernel.DetachPort` call. `--shutdown-timeout` fires; daemon force-exits; supervising systemd may issue SIGKILL after `TimeoutStopSec`. | Last-resort `systemctl kill -s SIGKILL usbip` if supervisor doesn't escalate. Investigate the wedged kernel module separately. |

The drain gate does NOT reset on `src.Drain` failure: the design
treats the first drain attempt as final because the daemon will
exit either way. An operator who needs more time should bump
`--shutdown-timeout` on the running daemon (not currently a live-
reloadable flag) by killing it and restarting with a higher value.

## Permissions

UDS file mode `0660`, owned by the configured
`--status-socket-group` (default `usbip-go`). Operators added to
that group can drain the daemon without root. The status endpoint
exposes only the daemon's own state — no kernel writes flow through
the UDS.

See [`docs/ops.md`](../ops.md) — "Status UDS" and "Two timeouts,
server-authoritative" sections for operational guidance.
