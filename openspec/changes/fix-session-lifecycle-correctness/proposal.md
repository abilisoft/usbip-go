## Why

Exporter sessions can outlive their real kernel connection, disappear from bookkeeping before kernel cleanup, and report successful shutdown after failed disconnects. Concurrent importer detach calls can also act on a later attachment that reused the same VHCI port, violating the documented concurrency-safe lifecycle.

## What Changes

- Observe exporter session completion through both lifecycle events and a deterministic `usbip_status` polling backstop.
- Keep handed-off exporter sessions owned independently of the `Serve` context until peer completion or explicit `Shutdown` cleanup.
- Record exactly one exporter disconnect attempt and its result per session, aggregate independent failures, and make repeated `Shutdown` calls observe the same completion result.
- Share one detach attempt per importer attachment, allow waiting callers to cancel without cancelling the owner, and remove a handle only when its exact attachment generation completed successfully.
- Serialize Linux adapter attach and detach topology/sysfs mutations, reserve an adapter-selected PortID before kernel handoff, and atomically replace that reservation with the exact published handle.
- Preserve teardown intent across a bounded Attach-to-publication wait so a late successful handoff is compensated rather than becoming live after Detach returned not-found.
- Add deterministic concurrency, fake-clock, and error-propagation regression coverage.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `exporter-daemon`: Define peer-completion detection, `Serve`-independent handed-off session ownership, exactly-once cleanup, and truthful repeatable shutdown results.
- `importer-lifecycle`: Define one shared detach attempt per attachment generation, caller-local cancellation semantics, and detach-safe handle publication.
- `kernel-adapter`: Require attach and detach mutations to share one VHCI serialization boundary, reserve the selected port before mutation, and expose exporter session activity from `usbip_status`.

## Impact

The change affects exporter and importer lifecycle coordination in `internal/app`, a narrow exporter-activity capability, the Linux kernel adapter, focused test doubles, deterministic lifecycle tests, accepted OpenSpec specifications, and traceability. Public Go signatures and USB/IP wire behavior remain compatible.
