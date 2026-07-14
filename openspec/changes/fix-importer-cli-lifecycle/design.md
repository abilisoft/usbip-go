## Context

`usbip-go attach` is intentionally one-shot when auto-reconnect is disabled: after fd handoff, the kernel owns the live Attachment and the process exits. A later `port` or `detach` command therefore constructs a fresh Importer with no process-local handle. Today `ListPorts` invents remote identity from VHCI's `local_busid`, while `Detach` rejects the live kernel port because the handle map is empty.

The public v1 `Port` shape already separates remote `BusID`/`Remote` from `LocalBusID`. Linux VHCI status retains the local busid, DeviceID, speed, status, and PortID, but not the exporter endpoint or remote topology string. The fix must preserve that public shape, the adapter layering, strict tracked-generation race guarantees, and truthful output without adding an unaudited state store during release preparation.

## Goals / Non-Goals

**Goals:**

- Make one-shot CLI attach, later port inspection, and later detach work across process boundaries.
- Keep local and remote Port identities semantically correct.
- Allow untracked detach without weakening handle-generation, reconnect, reservation, retry, or Close coordination.
- Preserve every public Go type and JSON field.
- Prove the behavior with unit, race, and live-kernel regression coverage.

**Non-Goals:**

- Persist exporter endpoint or remote BusID across processes.
- Change the USB/IP wire protocol, kernel ABI, or public Go source surface.
- Add TLS, authentication, or cross-process lifecycle locking.
- Make stale userspace metadata authoritative over live VHCI state.

## Decisions

### Keep kernel-derived metadata local and explicit

The kernel adapter will populate `LocalBusID` from VHCI and leave `BusID` and `Remote` at their zero values. VHCI events follow the same rule. Rendering will treat a zero-host RemoteEndpoint as unknown and emit an empty string rather than `:3240`.

Alternative: copy `local_busid` into `BusID`, as today. Rejected because it publishes a false exporter identity and violates the existing v1 field meanings.

Alternative: add a durable metadata file now. Rejected because port reuse has no kernel generation token; a safe cache needs locking, reconciliation, symlink defenses, bounded parsing, and atomic lifecycle rules beyond this release fix. Empty unknown values are truthful and source-compatible.

### Enrich only from an exact live Importer handle

`Importer.ListPorts` will retain the authoritative kernel status, speed, DeviceID, PortID, and LocalBusID. While holding the importer read lock, it may overlay only remote BusID and Remote from the currently mapped handle when the row is used and its DeviceID and speed match the handle's last successful Port snapshot.

Alternative: return the handle snapshot wholesale. Rejected because kernel state remains authoritative after asynchronous detach, module reload, or external mutation.

### Reconcile and detach under the kernel mutation boundary

`ImporterAdapter.DetachPort` will read the fresh VHCI status topology, validate the flat PortID, confirm the row is non-free, and write detach while holding `portMutationMu`. An absent or free row returns the canonical not-bound sentinel without a sysfs write.

Alternative: call `ListPorts` in the application and then `DetachPort`. Rejected because the two calls leave a state-of-check/state-of-use window inside one process.

### Coordinate untracked detach separately from reconnect handles

Importer will maintain a per-Port map of ephemeral untracked detach attempts. The first caller owns the kernel operation, followers share its result with context-aware waiting, Close observes the operation through the lifecycle wait group, and failure removes the attempt so a later call can reconcile again. `reserveAttachPort` rejects a selected PortID while an untracked detach attempt owns that transition.

Alternative: fabricate a `portHandle`. Rejected because an externally or previously attached port has no truthful reconnect strategy, endpoint, BusID, or generation snapshot.

Alternative: bypass the application layer in the CLI. Rejected because it duplicates lifecycle policy and leaves public `Importer.Detach` inconsistent with the CLI.

### Identify the live integration port from attach output

The CLI integration will request the JSON attach acknowledgement, retain its PortID for cleanup and detach, and poll `port --id` for the used state. It will not compare the exporter busid with importer-local VHCI topology.

### Retry each VHCI event once with a fresh event-local topology

For a VHCI-shaped event, the mapper will attempt fresh topology discovery and coordinate validation. If either fails, it will perform at most one complete retry for the same event, rediscovering topology and repeating coordinate validation before flat-Port conversion. Neither snapshot nor retry state survives the mapper call. Non-VHCI usbip-host events continue to bypass VHCI topology discovery.

Alternative: rely on a later event to recover. Rejected because uevents are one-shot and are not replayed.

Alternative: cache successful topology or retry state across events. Rejected because `vhci_hcd` reload can change controller, hub-width, and BusMap coordinates.

Alternative: retry indefinitely. Rejected because event dispatch must remain bounded.

## Risks / Trade-offs

- **Fresh-process port output omits remote endpoint and busid** → Document the limitation and preserve empty JSON fields; never substitute false local data.
- **PortID reuse races with untracked detach** → Serialize kernel inspection/mutation and reject same-Importer attach reservation while the untracked transition is owned.
- **A separate process can still race attach/detach** → This change does not claim cross-process locking; live kernel validation narrows the operation to the requested currently attached PortID, matching one-shot CLI expectations.
- **Same-process enrichment could meet a reused slot** → Require current handle identity plus used status, DeviceID, and speed; retain kernel fields as authority.
- **Lifecycle coordination regresses tracked handles** → Keep the tracked branch unchanged and add focused concurrent/race tests around the new untracked branch.
- **Retry adds sysfs work to a failing event** → Retry only that VHCI event once with a fresh snapshot and never enter this path for exporter-only events.
