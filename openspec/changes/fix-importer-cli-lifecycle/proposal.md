## Why

The real kernel integration flow shows that one-shot `usbip-go attach` leaves a live VHCI port that a later `usbip-go detach` process cannot release, because detach incorrectly treats process-local handles as the only authority. The same flow also exposes false port metadata: VHCI's importer-local busid is reported as if it were the exporter remote busid. A one-shot VHCI event can also be lost permanently when its first fresh topology discovery or coordinate validation transiently fails; a later event is not a retry because uevents are not replayed.

## What Changes

- Allow an Importer with no process-local handle to reconcile and detach a port that authoritative VHCI state reports as attached.
- Deduplicate untracked detach attempts and preserve the existing tracked-handle, reconnect-generation, reservation, retry, and Close coordination guarantees.
- Keep VHCI-derived `local_busid` separate from remote `busid`; leave unavailable remote endpoint and busid values empty rather than inventing them.
- Enrich kernel port rows with remote metadata only when the current Importer owns the exact live handle generation.
- Render unknown remote endpoints as an empty string, not the normalized-looking but false `:3240` value.
- Make the CLI kernel integration capture the attach acknowledgement's port ID and prove attach, port, and detach across separate processes.
- Document that durable cross-process remote metadata is outside this change; fresh-process port views remain truthful but may omit remote identity.
- Give each VHCI-shaped event at most one immediate retry that rediscovers topology and revalidates coordinates for that same event; drop it after the second failure, retain no retry or topology state across events, and keep exporter-only mapping free of VHCI discovery.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `importer-lifecycle`: Permit safe detach of kernel-visible attachments not tracked by the current Importer and define same-process metadata enrichment.
- `kernel-adapter`: Preserve the VHCI local/remote identity boundary, make untracked status validation plus detach one serialized mutation, and provide one bounded, fresh, event-local VHCI mapping retry without affecting exporter-only laziness.
- `json-contracts`: Define empty `remote` and `busid` fields when only kernel-local attachment metadata survives.
- `cli-interface`: Require one-shot attach, later port inspection, and later detach to interoperate across process boundaries.
- `security-release-quality`: Require the live CLI integration flow to identify the acknowledged port exactly and complete without identity-assumption skips.

## Impact

The change affects importer lifecycle coordination, the Linux VHCI adapter and event mapping, public `Port` value semantics without changing its Go shape, CLI port rendering, integration tests, OpenSpec traceability, and operator/JSON documentation. It adds no dependency, wire-protocol change, security bypass, or durable state file.
