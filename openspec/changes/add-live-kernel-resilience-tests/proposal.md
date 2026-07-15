## Why

The tracked live-kernel integration suite proves repeated importer reuse and
post-handoff URB traffic only inside one kernel using loopback TCP. A local
two-guest KVM prototype has also proved one production USB/IP transfer, but it
is ignored workspace state rather than a repository test, performs only one
attach/detach cycle, and does not exercise controlled network latency. This
leaves distinct-kernel resilience, repeated production lifecycle behavior, and
real delayed-network handling dependent on ad hoc manual evidence.

The abrupt-daemon-restart regression also relied on an interrupted handoff. A
stable handoff shows that exporter and importer kernels can retain their socket
references after the exporter process dies, so recovery must reconcile the old
exporter session before expecting the exact client Port to become free.

Final fail-closed review found three additional false-success paths: Linux's
controller-not-ready VHCI placeholder could masquerade as claimed
`NotAssigned` Ports, single-kernel drain checks could accept unreadable or
malformed state, and the two-guest runner could scan incomplete diagnostics or
delete overlays without proving both guests had stopped.

Turning that prototype into a test also exposed a production lifecycle defect:
the short-lived `attach` process handed the connection to the kernel and exited,
but a later independent `detach` process rejected the kernel-owned Port because
its fresh Importer had no process-local handle. Kernel ownership must remain
authoritative across CLI process boundaries.

## What Changes

- Preserve the bounded single-kernel repeated-cycle and fixed-delay Go tests as
  focused prerequisites for importer and stream-proxy correctness.
- Make the abrupt-daemon-restart regression establish stable kernel handoff,
  reconcile any retained exporter session before waiting for the exact client
  Port to become free, and require a fresh session on the replacement daemon.
- Distinguish ordinary claimed `NotAssigned` rows from Linux's exact
  sixteen-zero controller-not-ready placeholder, failing listing and allocation
  closed instead of exposing synthetic Ports.
- Make single-kernel drain evidence require filesystem `ErrNotExist`, a
  nonempty structurally valid all-`Null` VHCI snapshot, exact-Port release that
  excludes `NotAssigned`, and successful gadget cleanup.
- Add a tracked Bazel-backed Make workflow that boots distinct exporter and
  importer KVM guests and exercises the production bind, serve, attach, and
  detach paths across their separate kernel instances.
- Require exactly three cycles. Each cycle transfers unique deterministic ACM
  payloads byte-for-byte in both directions, detaches the exact returned Port,
  and fails closed unless importer Port/device state and exporter session state
  drain before the next attach.
- Let a fresh Importer detach a valid kernel-owned Port created by an earlier
  Importer or CLI process, while preserving shared-attempt, retry, Close, and
  Port-reservation lifecycle guarantees.
- Keep public `ListPorts` as the authoritative kernel-capacity view while CLI
  `port` output and detach completion expose only active Port states.
- Classify an in-range detach sysfs write returning `EINVAL` as the canonical
  already-free/not-bound sentinel without reclassifying unrelated `EINVAL`
  failures.
- Apply exactly 25 ms of `tc netem` delay to both dedicated guest egress paths and prove
  both directional payloads cross the impaired inter-guest connection through
  advancing byte and packet counters.
- Cap each guest at one vCPU and 1024 MiB of memory, pin and verify the guest
  image, and make KVM, QEMU, image acquisition, and test networking an explicit
  narrow manual non-hermetic exception.
- Keep the two-guest workflow out of GitHub-hosted automation, whose runners do
  not provide the required KVM, kernel-module, configfs, or networking surface.
- Require both guests to remain alive while successful nonempty kernel,
  journal, system, and role evidence is captured before scanning, and preserve
  run state and overlays unless every guest is confirmed stopped.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `developer-workflow`: Expose the tracked two-guest test through a dedicated
  Make entrypoint backed by a local/manual Bazel target with explicit host
  prerequisites, strict success evidence, and fail-closed overlay preservation.
- `exporter-daemon`: Specify that abrupt process death can leave a handed-off
  kernel session retained, that exporter reconciliation precedes exact client
  Port release, and that a replacement daemon cannot inherit the old session.
- `cli-interface`: Make independent `attach` and `detach` invocations work
  across process boundaries, keep CLI Port views limited to active Ports, and
  never render controller-not-ready placeholders.
- `importer-lifecycle`: Require both bounded same-Importer regression coverage
  and exactly three independent-process production lifecycle cycles across
  distinct guest kernels, with authoritative exact-Port and kernel-drain proof.
- `kernel-adapter`: Map detach-specific already-free kernel rejection to the
  stable not-bound sentinel without broad errno reclassification, and reject
  controller-not-ready status snapshots without hiding genuine claimed Ports.
- `public-library-api`: Permit a fresh Importer to detach a Port that remains
  owned by the kernel and clarify the fail-closed raw kernel-capacity
  `ListPorts` view.
- `release-packaging`: Preserve both kernel-integration workflows as explicit
  manual maintainer evidence outside hosted release automation.
- `transport-networking`: Require deterministic proof that production USB/IP
  remains correct through the focused 20 ms-per-chunk proxy and when both
  directions of the inter-guest network carry exactly 25 ms of egress delay.
- `security-release-quality`: Treat the bounded single-kernel prerequisites and
  the resource-capped, fail-closed two-guest workflow as explicit integration
  quality requirements, including strict drain, diagnostic, and cleanup
  evidence, without scheduling privileged KVM work on hosted CI.

## Impact

The change affects importer detach behavior, CLI active-Port presentation,
kernel error classification, daemon-restart and resilience integration tests, a
tracked KVM orchestration script, Bazel/Make wiring, test-only guest
provisioning and networking, operator and developer documentation, OpenSpec
requirements, and traceability.
The public Go API shape, USB/IP wire format, production runtime dependencies,
security model, and release assets remain unchanged.
