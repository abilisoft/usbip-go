## Why

Several CLI control-plane paths can report or imply clean completion without
actually owning all of the resources involved. In particular, the status socket
can disappear before exporter shutdown returns, accepted status HTTP
connections can outlive listener closure, incomplete status JSON can look idle
through Go zero values, unselected activation listeners can leak, and concurrent
history updates can lose records or preserve permissive file modes.

## What Changes

- Make `runDaemon` the sole owner of one bounded `Exporter.Shutdown` call and
  keep the status UDS available until that call returns.
- Cancel status request contexts, gracefully close accepted HTTP connections,
  and force-close any remainder before the status server returns.
- Make the drain client require a successful schema-v1 status response with
  explicitly present `sessions` and `listening.accepting` fields.
- Close every systemd activation listener not selected for the USB/IP accept
  loop.
- Serialize attach-history read-modify-write transactions with a sidecar flock,
  reject substituted history/lock symlinks, correct existing file permissions,
  and atomically replace complete history files without reopening private
  temporaries by pathname.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `operations-observability`: daemon shutdown, status-server connection
  ownership, and activation-listener ownership are explicit.
- `cli-interface`: drain response validation and private concurrent history
  updates fail safely.

## Impact

- Changes only internal CLI and daemon behavior; no public Go API or JSON
  producer schema changes.
- Adds no dependency and preserves schema-v1 additive compatibility.
- Updates focused unit/integration regressions, operator documentation, accepted
  OpenSpec, and traceability evidence.
