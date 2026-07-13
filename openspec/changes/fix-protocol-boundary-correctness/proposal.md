## Why

Several boundary paths accept or create invalid state: an exact devlist reply can
block after its complete count-delimited frame, VHCI topology remains cached
across module reload, application code can replace a tighter transport read
deadline, jittered exponential retries can exceed their maximum or overflow,
and RemoteEndpoint parsing accepts ambiguous invalid forms while
rejecting valid scoped IPv6 literals. These defects affect availability and
correctness at protocol, kernel, transport, and public API boundaries.

## What Changes

- Detect only already-buffered bytes after a complete devlist frame so exact
  replies never wait for an additional byte, while retaining advisory trailing
  data reporting and effective full-frame fuzz seeds.
- Discover fresh operation-local VHCI topology snapshots, use one consistent
  snapshot throughout each attach selection/validation sequence, and validate
  every VHCI event's controller and root Port against its fresh snapshot.
- Leave configured handshake read deadlines owned by the transport adapter;
  application cancellation continues to interrupt blocked I/O by closing the
  connection without extending an earlier deadline.
- Preserve v1's non-negative exponential backoff bounds, including zero-delay
  schedules, while capping the final jittered result safely at Max before
  converting it to `time.Duration`.
- Parse RemoteEndpoint strings with an unambiguous grammar: reject explicit
  empty ports, accept scoped IPv6 literals, require bracket contents to be IPv6,
  and enforce the 253-byte DNS hostname bound.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `wire-protocol`: complete count-delimited devlist frames return without
  probing the underlying stream; fuzz seeds exercise full frames.
- `kernel-adapter`: VHCI topology is fresh per operation or relevant event, one
  attach uses a consistent snapshot, and stale/malformed event coordinates are
  dropped before flat-Port arithmetic.
- `transport-networking`: transport-configured read deadlines remain
  authoritative while application cancellation closes the connection.
- `importer-lifecycle`: remote listing and import handshakes do not replace a
  tighter transport deadline with the caller context deadline.
- `public-library-api`: exponential backoff preserves non-negative v1 bounds
  and always returns a value bounded by Max.
- `domain-model`: RemoteEndpoint parsing accepts scoped IPv6 and rejects
  ambiguous empty-port, non-IPv6 bracket, and overlong hostname forms.

## Impact

- Affects `internal/adapter/wire`, `internal/adapter/kernel`, `internal/app`,
  `pkg/usbip`, and `pkg/domain`, plus focused tests, fuzz seeds, Bazel manifests,
  main OpenSpec requirements, and traceability evidence.
- Preserves the public v1 source and zero-bound backoff behavior; endpoint
  parsing becomes stricter only for ambiguous or invalid strings.
- Adds no dependency and does not change the USB/IP wire format, kernel ABI,
  plaintext transport security model, or post-handoff kernel ownership of URB
  traffic.
