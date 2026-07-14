## Context

The exporter currently equates its `Serve` request context and synthetic uevents with kernel session lifetime. Linux `usbip_host` instead consumes peer EOF internally and changes `usbip_status` from used to available without emitting the VHCI detach event used by the importer. Consequently normal peer completion can leave a registered exporter Session forever, while cancelling `Serve` can do the opposite: unregister a still handed-off Session before `Shutdown` can release it. Cleanup errors are logged but discarded.

Importer handles are generation-bearing pointers, but `Detach` does not assign an owner to a generation. Two callers can therefore issue duplicate sysfs detaches, and a delayed completion can delete a newer attachment that reused the same PortID. Adapter attach serialization also does not cover detach, so VHCI topology checks and sysfs mutations can overlap.

The public v1 API and USB/IP wire contract must remain compatible. Tests must use explicit channels and the injected Clock, not scheduler sleeps.

## Goals / Non-Goals

**Goals:**

- Give every handed-off exporter Session one explicit terminal owner and one stored cleanup result.
- Detect normal exporter peer completion from authoritative `usbip_status` without depending on a nonexistent uevent.
- Make `Shutdown` attempt every required disconnect, join independent errors, and return the same completed cleanup failures to repeat callers.
- Give every importer attachment generation at most one active detach attempt whose result is shared by overlapping callers.
- Prevent attach and detach kernel mutations from overlapping, prevent a stale detach completion from deleting a newer attachment that reused its PortID, and make an adapter-selected port visible to Detach before it becomes kernel-live.

**Non-Goals:**

- Changing public method signatures, wire framing, or kernel ABI payloads.
- Making inherently synchronous sysfs writes cancellable after the kernel mutation begins.
- Replacing uevents; they remain the low-latency completion signal and polling remains a backstop.
- Changing reconnect policy, backoff, or public event vocabulary.

## Decisions

### Store per-session cleanup completion and error

Each exporter `sessionHandle` owns a completion channel plus an error protected by its handoff mutex. The existing handoff state remains the election point: cancellation before handoff completes requires no disconnect, cancellation after a successful handoff claims one disconnect, and cancellation during handoff lets the completing handler claim cleanup only if the handoff succeeds. Natural peer completion claims terminal completion without a disconnect. Exactly one claimant closes the completion channel.

This is preferred over a global disconnect WaitGroup because late successful handoffs can schedule cleanup after an initial global snapshot. Per-handle futures also let every `Shutdown` call observe the same immutable result.

### Retain the first shutdown handle snapshot

The exporter stores the first shutdown's Session handles and returns that same slice to later callers. First shutdown signals cancellation and starts already-required disconnects; repeat calls never repeat a kernel mutation, but wait for and join the retained handles' stored results. Session drain and cleanup errors are joined so a timeout cannot hide an already-completed disconnect failure.

This is preferred over logging errors or returning only the first failure because shutdown success must prove all required cleanup and callers need `errors.Is` access to every independent cause.

### Detach handed-off Session lifetime from Serve cancellation

The event subscription and post-handoff wait use `context.WithoutCancel` so cancellation of the accept-loop context stops new work but does not terminate kernel-owned work. `handle.done`, closed only by `Shutdown` or final unregister, remains the explicit termination signal. Subscription closure disables the event source rather than declaring the kernel Session ended.

### Poll authoritative exporter activity

The app defines a narrow optional `exporterSessionActivity` capability with `ExportSessionActive(ctx, busID)`. The production Linux exporter adapter implements it by reading and parsing the per-device `usbip_status` attribute, reporting active only for `SDEV_ST_USED`. Keeping the capability separate from the broad generated `ExporterKernel` contract leaves the generator-owned mock untouched; focused polling tests use a small wrapper that embeds that mock and implements only the extra probe. Existing kernels without the capability retain event/Shutdown observation, while the production adapter enables the authoritative polling backstop.

After successful handoff, a capable kernel arms repeated checks through the injected Clock; an available/non-used result terminates the Session as client-gone, while read or parse errors are logged and retried. Role-correct local `DeviceUnboundEvent` values remain active in parallel for low latency. Importer-side `PortDetachedEvent` values are ignored even when their remote BusID string collides with the exported local BusID; the shared event stream does not make BusID a cross-role identity.

A fixed semantic poll interval avoids a new public configuration surface. Deterministic tests inject `FakeClock` and advance that interval explicitly.

### Share one importer detach attempt per handle

Each `portHandle` owns a mutex-protected pointer to the active detach attempt. The first caller becomes owner, enrolls the attempt in the Importer lifecycle wait group, cancels and drains the watcher, and performs the kernel mutation. Overlapping callers wait on the same result channel. A waiting caller may return its own context error without affecting the owner's context or the shared operation. A failed attempt is cleared after publishing its result so a later caller can retry.

### Serialize adapter mutations and reserve handle publication

The Linux `ImporterAdapter` expands its existing attach critical section to cover `DetachPort`. This one adapter-owned VHCI mutex serializes the topology checks and sysfs mutations in `AttachRemote` and `DetachPort`, including reconnect rollback calls that use the same adapter method.

Adapter-only mutation serialization is insufficient for application bookkeeping: after a successful sysfs write, the kernel can own a live port before `finishAttach` publishes its `portHandle`. A concurrent `Detach` in that interval previously observed neither state, returned not-found, and lost the teardown request.

`RemoteDeviceSpec` therefore carries a narrow selected-port reservation callback. `AttachRemote` invokes it synchronously after free-port selection and before the sysfs write. The callback installs a per-Port reservation under the importer mutex, then releases that mutex before the potentially wedged kernel handoff. Successful `finishAttach` atomically replaces the exact reservation with the exact handle; failure removes and completes only that reservation.

When `Detach` sees a reservation but no handle, it records teardown intent and waits outside the importer mutex, bounded by the Attach shutdown timeout and its own context. A successful publication stores the handle's shared detach future on the reservation before waking every caller, so even a fast compensation that removes the handle cannot make a waiter lose the completed result. If the wait times out or is cancelled, the recorded intent remains: a later successful publication starts a cancellation-independent compensating detach against the exact handle, while preserving that handle if kernel teardown fails. This prevents a late handoff from becoming untracked without putting an application-wide lock around kernel I/O.

Reconnect rollback uses that same exact-handle detach ownership. The shared unexported attach path returns the exact published handle beside the Port; public `Attach` discards that ownership token without changing its signature, while reconnect completion carries it directly into rollback. Rollback never rediscovers ownership through the reusable PortID, BusID, or generation number. If a Detach already claimed the replacement during its publication window, rollback neither races it nor automatically retries a failed compensation; the retained handle remains available for a later explicit retry. If rollback wins first, a concurrent public Detach joins its shared result. Thus every replacement generation has at most one active kernel mutation even when old-handle rollback and new-handle compensation overlap.

A same-PortID reconnect needs an additional per-Port election because the old handle and replacement reservation can coexist. Both transitions run under the importer mutex: if the reservation exists first, Detach prioritizes it, marks teardown intent, and cancels the predecessor without issuing an old-generation kernel mutation; publication then owns exact replacement compensation. If Detach marks the old handle detaching first, a later reservation callback is rejected before the adapter writes sysfs. This avoids holding an application mutex across kernel I/O while ensuring an old pointer check can never wait behind AttachRemote's adapter mutex and then mutate the new generation.

When reservation teardown intent exists, the replacement is published without starting another reconnect watcher. Its compensating detach future is already the terminal owner, so creating a watcher would add an unnecessary cancellation/drain cycle and expose state the user has explicitly asked to remove.

For already-published handles, a detach owner still snapshots the exact `portHandle` pointer, rechecks that `handles[id]` contains that pointer before the kernel call, and conditionally deletes only that pointer after success. The adapter lock protects kernel mutation overlap; reservations protect the pre-publication interval; exact-pointer checks protect application bookkeeping from PortID reuse.

### Reset reconnect backoff before replacement watcher activation

Reconnect attempts reuse the configured `BackoffStrategy` across watcher generations. A successful reconnect marks its recursive Attach as an internal reconnect path; after exact-handle publication and before spawning the replacement watcher, the completing old watcher calls `Reset`. This ordering prevents an immediate second detach from invoking the replacement watcher's `Next` concurrently with `Reset`, which user-supplied stateful strategies are not required to make concurrency-safe. Rollback/compensation paths do not start a replacement watcher and therefore do not reset a strategy for a user-invisible replacement.

## Risks / Trade-offs

- **[Risk] Polling adds periodic sysfs reads per active exporter Session.** → Use one modest fixed interval, retain uevents for prompt completion, and stop polling immediately on terminal ownership.
- **[Risk] A persistent status-read error retains a Session until explicit Shutdown.** → Log each failed probe and prefer a retained, cleanable kernel owner over prematurely unregistering potentially active state.
- **[Risk] A synchronous kernel disconnect can ignore context.** → Keep the existing bounded Shutdown wait and force-close behavior; store the pending per-handle future so later calls can observe eventual completion.
- **[Risk] Adapter serialization can make one VHCI mutation wait behind another synchronous sysfs operation.** → Keep the boundary inside the Linux adapter and limited to topology checks plus attach/detach writes; do not extend it through application handle publication or ordinary USB/IP handshakes.
- **[Risk] Detach can wait on a kernel handoff that never returns.** → Wait outside the importer mutex, apply the attachment's shutdown bound and caller cancellation, and retain compensating teardown intent after the waiter exits.
- **[Risk] Compensating kernel teardown can fail.** → Publish the exact handle before compensation and retain it on failure so later Detach calls can retry; never discard the only application owner of a live port.
- **[Risk] Failed detach cancels its old reconnect watcher.** → Preserve existing behavior and retain the handle so a later explicit detach can retry; do not silently restart a cancelled watcher.

## Migration Plan

No data or public API migration is required. Land implementation, generated mocks, deterministic unit/race tests, accepted specifications, and traceability atomically. Rollback is a normal code revert because no persisted format changes.

## Open Questions

None.
