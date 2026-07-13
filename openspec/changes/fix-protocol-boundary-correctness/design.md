## Context

The affected paths sit at independent trust and lifecycle boundaries but share
one failure mode: a locally convenient second observation can invalidate or
extend the first. `DecodeOpRepDevlist` currently probes an underlying stream
after decoding its count-delimited frame; importer operations overwrite the
deadline already installed by the transport; VHCI helpers and event mapping
reuse successful topology observations indefinitely; attach selects a Port from
one topology observation and validates it against another; jitter is applied
after the base is capped; and RemoteEndpoint parsing loses whether a port
delimiter was explicitly present.

The public v1 Go surface must remain source compatible, the domain package must
remain standard-library-only, and real topology data is a small bounded sysfs
tree whose reads are negligible beside network and kernel handoff work.

## Goals / Non-Goals

**Goals:**

- Make a complete devlist decode return immediately on an open stream.
- Make every kernel operation and relevant VHCI event observe current topology,
  without allowing one attach operation to mix snapshots or a delayed event to
  be remapped through a different controller/root-Port geometry.
- Preserve the earliest configured handshake read bound while retaining prompt
  caller cancellation.
- Preserve v1's accepted zero-bound exponential schedules while making every
  emitted delay no greater than Max, including near `time.Duration` limits.
- Accept valid scoped IPv6 and enforce an unambiguous, DNS-bounded endpoint
  grammar.
- Pin each correction with deterministic regressions and keep main OpenSpec and
  traceability evidence synchronized.

**Non-Goals:**

- Adding a length prefix to USB/IP, waiting for trailing bytes that arrive after
  a frame is complete, or changing permissive handling of bytes already read.
- Polling or watching for topology generation changes, retaining successful
  cross-operation topology caches, or changing the kernel ABI.
- Replacing transport deadlines with context deadlines or adding TLS.
- Changing FixedBackoff's intentionally usable zero delay.
- Changing ExponentialBackoff's v1-compatible zero-Min immediate retries.
- Adding IDNA conversion or accepting Unicode DNS labels.

## Decisions

### Treat only buffered bytes as devlist trailing data

The decoder continues to wrap the input in `bufio.Reader`, but after reading the
declared devices it checks `Buffered() > 0` rather than calling `Peek(1)`. The
buffer may already contain bytes fetched alongside the declared frame, so the
existing warning still catches immediately adjacent trailing data. It never
causes another underlying read after the frame is complete.

Alternatives rejected:

- `Peek(1)` cannot distinguish an exact live frame from a peer that might send
  more later and therefore waits for data, EOF, or a deadline.
- Type-specific `Len`/nonblocking socket probes do not work for generic
  `io.Reader` and would make behavior adapter-dependent.
- Removing the advisory entirely loses useful malformed-peer diagnostics.

Fuzz seeds include complete USB/IP reply headers and counts so they enter the
body decoder rather than failing at `DecodeHeader` first.

### Discover topology at the operation or event boundary

`loadTopology` and `loadStatusTopology` become thin fresh-discovery wrappers;
successful snapshots are not retained across calls. Status parsing gains a
snapshot-taking helper. `AttachRemote`, under its existing allocation mutex,
loads one status snapshot and passes it through status-row reading, free-port
selection, and pre-write bounds validation. List, Detach, test-only direct
attach, and each VHCI-shaped event load their own fresh snapshot. Event mapping
also checks the devpath controller suffix against the fresh BusMap location and
requires the root Port to lie in `[1,HCPorts]` before calling `FlatPort`.
Exporter-only USB driver events continue to bypass VHCI discovery.

Alternatives rejected:

- Successful forever-caches cannot observe `vhci_hcd` unload/reload or changed
  controller/port/bus mappings.
- Inode, timestamp, or generation invalidation is filesystem- and
  kernel-specific and more complex than bounded sysfs rediscovery.
- Loading separately before selection and before validation allows one attach
  to combine two legitimate but incompatible module generations.

### Keep read-deadline policy in the transport adapter

Importer application code stops calling `SetReadDeadline` from the context
deadline. The transport already installs its configured handshake deadline.
Both listing and attach retain a cancellation watcher that closes the connection
on `ctx.Done()`, so a deadline-free cancelled context still interrupts a blocked
read. Deterministic regressions block inside Read until Close, proving both
watchers interrupt I/O without calling `SetReadDeadline`. This prevents a later
caller deadline from extending an earlier transport deadline without introducing
a second timer policy.

### Cap jitter before duration conversion

The base delay remains geometrically capped. After sampling a multiplier, the
code compares the float result with `float64(Max)` and returns Max before
converting if the result reaches or exceeds it. This simultaneously enforces the
documented final cap and avoids float-to-duration overflow near MaxInt64. Public
validation retains the v1 non-negative bounds: zero Min continues producing the
historical zero-delay schedule, and zero Max remains valid when Min is also zero.

### Preserve endpoint grammar facts during splitting

The split helper returns whether a colon-delimited port was present. An empty
value after an explicit delimiter therefore reaches port parsing and fails,
while a genuinely omitted port still defaults to 3240. Bracket parsing validates
its content with `net/netip.ParseAddr` and `Is6`; host validation also uses
`netip.ParseAddr`, which supports scoped IPv6 zones. DNS validation applies the
253-byte bound after removing one optional absolute-name trailing dot, then
retains the existing 63-byte ASCII label rules.

Alternatives rejected:

- `net.ParseIP` cannot parse scoped zones.
- Treating `host:` or `[::1]:` as an omitted port erases an explicit malformed
  field and can conceal configuration errors.
- Accepting bracketed hostnames/IPv4 conflicts with the bracket syntax reserved
  for IPv6 literals by host:port grammar.

## Risks / Trade-offs

- **[Fresh sysfs discovery adds bounded local I/O]** → Limit discovery to one
  snapshot per operation or relevant event; never perform it for exporter-only
  events.
- **[Buffered-only detection cannot report bytes that arrive later]** → The
  devlist count already defines the complete frame, and advisory trailing
  reporting must not trade availability for future-data speculation.
- **[Stricter endpoint validation rejects previously accepted input]** → Reject
  only ambiguous endpoint forms; preserve zero-delay FixedBackoff and
  ExponentialBackoff behavior and cover valid scoped IPv6.
- **[Cancellation close can race successful kernel fd handoff]** → Preserve
  the existing watcher stop channel and handoff ownership ordering; this change
  only removes deadline mutation.

## Migration Plan

1. Add focused red regressions for each boundary and update the delta specs.
2. Implement each correction without changing exported signatures.
3. Synchronize the accepted main specs and traceability rows.
4. Run focused unit and race tests, strict OpenSpec validation, then the normal
   repository validation matrix.

Rollback is a normal revert of the implementation, tests, and synchronized
specification changes. No data or wire migration is required.

## Open Questions

None.
