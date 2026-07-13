## Context

The facade translates public values and lifecycle calls into `internal/app`.
Several edge cases crossed that boundary without preserving the state needed to
make results deterministic: option zero values lost presence, subscriber closure
did not order buffered publication, listener creation preceded serve-state
reservation, and one mutable reconnect strategy could serve multiple logical
attachments. The public v1 surface constrains the repair: existing behavior and
the field layout of `AttachOptions` must remain source-compatible.

## Goals / Non-Goals

**Goals:**

- Make terminal state and cancellation results deterministic without changing
  ordinary successful behavior.
- Prevent listener, kernel, or network side effects after an earlier lifecycle,
  validation, or deduplication rejection.
- Give each logical reconnect lineage independent custom strategy state through
  an additive option while keeping legacy callers race-safe.
- Return complete, platform-consistent probe and event observations.
- Cover each concurrency boundary with deterministic synchronization and focused
  race tests.

**Non-Goals:**

- No USB/IP wire, URB, TLS, authentication, ACL, or kernel-handoff redesign.
- No removal or reinterpretation of existing public symbols.
- No fields added to public `AttachOptions`; external unkeyed literals must keep
  compiling.
- No attempt to clone an arbitrary v1 `BackoffStrategy`, because that interface
  has no cloning contract.

## Decisions

### Subscriber closure is a publication barrier followed by a bounded drain

Each subscriber serializes publish and close with its own mutex. Closing the
terminal channel while holding that mutex establishes that, once an iterator
observes closure, no later event can enter the subscriber buffer. The iterator
then drains the bounded buffer before returning. Caller cancellation remains an
immediate stop and does not drain.

Closing the event channel itself was rejected because a copied subscriber list
can still contain an in-flight sender and panic. Selecting between `done` and
the event channel without a barrier was rejected because selection is
nondeterministic when both are ready.

### Serve state is reserved before listener construction

`internal/app` owns a context-aware listener-factory seam. It atomically checks
terminal and overlapping state, creates one per-call accept-loop completion
channel, and stores a cancellation function before invoking the factory. The
public facade supplies `Transport.Listen` as that factory. `Shutdown` cancels
the reservation before waiting, and closes the listener too once installation
has completed.

Binding first and calling `Serve` second was rejected because it performs an
externally visible side effect before `ErrExporterShutdown` or
`ErrServeAlreadyRunning`. A facade-only precheck was rejected because it would
race the authoritative internal state.

### Numeric option presence is explicit and non-finite rates are invalid

Private configuration tracks whether the accept-rate option was applied.
Omission selects the documented default; explicit finite zero or a negative
value disables limiting. NaN and either infinity are rejected at construction
with `ErrAcceptRateLimitInvalid`, translated to the public sentinel before
adapter side effects.

Treating every zero as omission was rejected because zero is already documented
as the disable form. Allowing non-finite values into the token bucket was
rejected because those values do not represent an operator policy.

### Terminal Importer state has first error precedence

`Attach` first performs a read-locked closed check, then validates arguments,
then uses the existing write-locked attach reservation that rechecks closure.
This yields deterministic precedence after `Close` while keeping a racing Close
from admitting new work.

Removing the locked reservation recheck was rejected because a fast precheck
alone has a time-of-check/time-of-use race.

### Module probing starts from a complete Unknown map

Common code constructs all three canonical module keys as `Unknown` on every
platform. Linux updates one entry at a time and checks context before each
probe. Cancellation returns the partially observed full-shape map with a wrapped
context error; unsupported platforms return the unchanged full shape.

Returning only completed keys was rejected because an absent key cannot
distinguish unprobed state from a schema change.

### A factory owns logical-attachment backoff state

`BackoffFactory` and `WithImporterBackoffFactory` are additive. The facade
threads a lazy internal factory into a top-level auto-reconnecting Attach.
`internal/app` invokes it once only after lifecycle, argument, and deduplication
checks, then carries the resulting strategy through every replacement
generation. Explicit per-Attach `Backoff` wins. Known immutable library
strategies keep their direct adapters; arbitrary legacy shared custom
strategies are serialized with an Importer-owned mutex.

Adding a field to public `AttachOptions` was rejected for source compatibility.
Reusing the factory on every replacement generation was rejected because one
logical attachment must retain its retry state. Silently assuming all existing
custom strategies are concurrency-safe was rejected because v1 never required
that property.

## Risks / Trade-offs

- **A listener factory that ignores context can delay Shutdown.** → The internal
  contract requires context compliance and the public transport implementation
  honors it; focused tests cover cancellation during setup.
- **Legacy custom backoff calls may serialize independent attachments.** → This
  is the safest compatible behavior; callers needing independent mutable state
  can opt into `WithImporterBackoffFactory`.
- **Draining terminal buffers can yield several queued events after closure.** →
  The buffer is already bounded, and this preserves events that were accepted
  before the terminal barrier rather than extending publication.
- **Cancellation returns partial probe observations.** → Every key remains
  present, and unobserved entries remain explicitly `Unknown`.

## Migration Plan

The change is additive and requires no data migration. Existing callers retain
their behavior. Stateful custom-backoff users may adopt
`WithImporterBackoffFactory`; rollback consists of removing the additive option
and reverting the internal seams after restoring the previous API baseline.

## Open Questions

None.
