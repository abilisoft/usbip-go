## Context

The drain command uses disappearance of the daemon status UDS as one completion
signal. `runDaemon` previously canceled that server before its deferred exporter
shutdown ran, so a client could observe a missing socket while
`Exporter.Shutdown` was still in flight. Separately, closing only the UDS
listener did not own already-accepted HTTP connections or request handlers.

The drain status decoder used value fields, making omitted required JSON fields
indistinguishable from legitimate empty or false values. Systemd activation
hands ownership of every returned listener to the caller, while attach history
is shared by independent CLI processes and therefore requires a cross-process
transaction boundary.

## Goals / Non-Goals

**Goals:**

- Make status-socket disappearance truthful evidence that the daemon's bounded
  exporter shutdown attempt has returned.
- Close active and idle accepted status connections and cancel their handler
  contexts before `serveStatus` returns.
- Reject status responses that cannot prove drain completion.
- Resolve ownership of every activation listener returned by systemd.
- Preserve every retained concurrent history update while maintaining private
  file permissions and complete-file reads.

**Non-Goals:**

- Changing the schema-v1 status document or public Go API.
- Making the drain client's timeout override the daemon's authoritative
  shutdown timeout.
- Adding a portable lock abstraction for non-Linux execution; the CLI daemon
  already targets Linux USB/IP hosts.

## Decisions

### Give `runDaemon` exclusive shutdown ownership

`statusExporter.Drain` marks the listener non-accepting and cancels `Serve`, but
does not call `Exporter.Shutdown`. `runDaemon` wraps the one bounded shutdown
call in `sync.OnceFunc`, invokes it while the detached status-server context is
still live, and cancels the status server only after shutdown returns. The defer
uses the same once-guarded function for startup-error paths.

Daemon-level integration regressions inject a per-invocation exporter through a
consumer-owned interface while retaining the real `runDaemon` listener and
status-server composition. They prove success, returned-error, and deadline
outcomes, exactly one shutdown call, and UDS responsiveness until shutdown
returns rather than relying on a helper-only ordering test.

This is preferable to coordinating two possible shutdown callers because it
provides one ordering point and prevents the status handler goroutine from
outliving the daemon's cleanup owner.

### Shut down the complete status HTTP server

`serveStatus` installs a derived `BaseContext` for every accepted request. Its
close coordinator cancels that context, calls bounded `http.Server.Shutdown`,
and falls back to `http.Server.Close` before reporting completion. Both active
handler and idle keep-alive regressions use channel or socket I/O ordering rather
than sleeps.

### Decode drain evidence with presence-aware fields

The client accepts only HTTP 2xx, schema `v1`, a non-null `sessions` array, and
a non-null `listening.accepting` boolean. Pointer fields preserve the distinction
between an explicit empty/false value and omitted/null data. Unknown additive
fields remain ignored, preserving schema-v1 forward compatibility.

### Resolve systemd listener ownership at selection time

When exactly one `usbip-go` listener is selected, every other listener returned
by `activation.ListenersWithNames` is closed immediately. Ambiguous listener
sets continue to close all listeners and fail rather than guessing.

### Lock and atomically replace history

A private `<history>.lock` sidecar is held with `flock` across the complete
read-modify-write transaction. Existing history and lock files are corrected to
mode `0600`. The new complete body is written to a private temporary in the same
directory and renamed over the destination, so readers observe either complete
generation. Kernel-owned flock release avoids stale lock files after crashes.

History and lock opens reject final-component symlinks. An existing history is
permission-corrected and read through one descriptor, and the private temporary
descriptor remains open through `fchmod`, write, and close before rename. This
avoids pathname-substitution races introduced by closing and reopening either
file. Failed temporary removal is joined with the primary storage error instead
of being discarded. The cross-process regression re-executes the test binary and
proves that a second process blocks on the production flock and preserves both
updates.

## Risks / Trade-offs

- **[Status remains visible longer during shutdown]** -> This is intentional;
  disappearance is delayed until the authoritative shutdown attempt returns.
- **[A status handler ignores cancellation]** -> Graceful shutdown is bounded
  and followed by force-close of accepted connections.
- **[History writers contend]** -> The file contains at most 20 records, so the
  serialized critical section is small and avoids lost updates.
- **[Atomic rename does not make storage power-loss durable]** -> The requirement
  is complete-file visibility and crash-released locking, not synchronous disk
  durability for completion hints.

## Migration Plan

No migration is required. The next history update tightens any existing history
and lock-file modes and replaces the prior history inode atomically.

## Open Questions

None.
