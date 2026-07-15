## Context

The in-process integration harness creates a configfs mass-storage gadget,
exports it through `usbip_vudc`, attaches it through a real `vhci_hcd`, and
verifies the resulting block-device bytes. The new focused Go regressions reuse
one Importer for exactly three fresh-gadget cycles and keep a fixed-delay TCP
proxy between both kernel file-descriptor handoffs. Those tests isolate
important lifecycle and stream behavior, but both kernels and the proxy still
run inside one guest over loopback.

A local two-guest prototype separately boots an exporter and importer, creates
an ACM configfs gadget behind `dummy_hcd`, binds that normally enumerated USB
device through `usbip_host`, and exercises the production CLI across the guest
network. It currently lives below the ignored `.local/` tree, performs one
cycle, and applies no controlled latency. It is useful design evidence but is
not a reproducible repository test.

The first tracked two-guest run exposed a production boundary that the
single-process tests did not cover. `usbip-go attach` successfully handed the
connection to `vhci_hcd` and exited, but the later `usbip-go detach` process
constructed a fresh Importer and rejected the Port because no process-local
attachment handle existed. The kernel still owned the Port, so local handle
absence was not authoritative evidence that detach was invalid.

A stable single-kernel daemon-restart run exposed the complementary exporter
boundary. Once both kernels own the handed-off socket, killing the exporter
process does not necessarily release either kernel reference. Waiting for the
client Port to become free before disconnecting the retained exporter session
therefore tests an impossible causal order rather than restart resilience.

The completed workflow must be deterministic, bounded, fail closed on missing
kernel evidence, and remain small enough for the memory-constrained KVM host.
It is intentionally manual because standard GitHub-hosted runners cannot expose
the necessary virtualization and privileged kernel surfaces.

## Goals / Non-Goals

**Goals:**

- Retain the focused single-kernel Go regressions as fast prerequisites for
  same-Importer reuse and bidirectional delayed-stream behavior.
- Make abrupt exporter restart coverage wait for stable handoff, reconcile the
  retained exporter session first, and require exact client-Port release before
  a fresh replacement attachment.
- Prove the production exporter and importer paths across two distinct guest
  kernel instances using a real configfs ACM gadget bound through `usbip_host`.
- Prove independent short-lived CLI processes can attach and later detach the
  same kernel-owned Port without weakening concurrent lifecycle guarantees.
- Complete exactly three attach, unique bidirectional byte-transfer, exact-Port
  detach, and fail-closed kernel/device-drain cycles.
- Prove both dedicated guest egress paths carry exactly 25 ms of `tc netem` delay while
  exact payload transfer continues in both directions.
- Track the orchestrator, Bazel target, Make entrypoint, provisioning inputs,
  and documentation in the repository.
- Bound final concurrent execution to one vCPU and 1024 MiB per guest and give
  every VM, process, socket, gadget, attachment, qdisc, and artifact an explicit
  cleanup owner.

**Non-Goals:**

- Soak, endurance, unbounded-cycle, throughput, or performance evidence.
- Packet loss, jitter, reordering, bandwidth limiting, blackholes, outages, or
  automatic reconnect behavior under impairment.
- Running KVM integration on standard GitHub-hosted runners.
- Making QEMU, KVM, SSH, image download, or inter-guest networking part of the
  routine hermetic build and unit-test graph.
- Changes to production networking, public API shape, release assets, or the
  USB/IP wire format.

## Decisions

### Preserve focused single-kernel prerequisites

The existing Go integration binary remains responsible for the smallest useful
live-kernel regressions. One test reuses a single Importer across exactly three
fresh VUDC mass-storage gadgets with distinct payloads. Another keeps a
race-safe 20 ms-per-chunk loopback proxy between `vhci_hcd` and `usbip_vudc` after
both file-descriptor handoffs and checks directional counter advancement.

These cases are prerequisites, not substitutes, for the two-guest workflow.
They localize importer or proxy defects without paying VM orchestration cost and
remain exposed by the existing `make test-integration` target.

### Reconcile retained kernel ownership before replacement attachment

The daemon-restart regression waits for the initial Attach and exporter
`usbip_status` to reach used before killing the serving process. Process death
closes userspace descriptors, but the exporter and importer kernels can retain
their handed-off socket references. Recovery therefore first reconciles the
exporter: an already-available session is complete, while a retained used
session is disconnected by writing `-1` to its `usbip_sockfd`.

Only after exporter reconciliation does the test wait for the exact old client
Port to become absent, `Null`, or `Available`. `NotAssigned` remains claimed and
cannot satisfy release. A replacement daemon on the same address cannot inherit
the old kernel-owned session; the client must complete a fresh Attach before the
replacement reaches used state.

### Use distinct exporter and importer guest kernels

The tracked orchestrator boots two guests simultaneously for final validation.
The exporter guest creates an ACM gadget on `dummy_hcd`, confirms that it is a
normally enumerated USB device, binds it through the production `usbip-go bind`
path, and runs the production `usbip-go serve` path. The importer guest loads
`vhci_hcd` and uses the production list, attach, port, and detach paths against
the exporter guest. Custom VUDC socket handoff code is not part of this case.

The roles use distinct guest kernel instances even when both images contain the
same kernel version. The inter-guest route must not collapse into loopback in
either guest. The runner records guest kernel versions, module readiness,
network addresses, command JSON, serial logs, and relevant kernel diagnostics.

Each guest uses one user-mode network interface only for host-to-guest SSH. A
second virtio network interface carries USB/IP directly between the guests over
QEMU's stream backend with static `/30` addresses. This avoids a host `nc`
relay whose process lifetime could retain the exporter TCP session after the
importer kernel detached. The runner proves the route to the peer uses that
dedicated non-loopback interface before applying impairment.

### Treat the kernel as detach ownership authority

Process-local attachment handles coordinate reconnect watchers and attachment
generations, but they are not the source of truth for whether `vhci_hcd` owns a
Port. When `Detach` receives a valid PortID with no local handle, the Importer
therefore invokes the serialized kernel detach mutation directly instead of
performing a `ListPorts` preflight. A read-before-write check would introduce a
TOCTOU race and still could not prove ownership between independent processes.

Within one Importer, overlapping untracked detaches for the same Port share one
attempt and result. A failed attempt clears only its coordination record so a
later call can retry. `Close` waits for an active untracked attempt, and attach
reservation for the same Port loses while that detach owns the mutation. The
existing tracked-handle path continues to cancel and drain its watcher. If the
kernel reports that a tracked Port is already free, the exact stale handle is
removed so a later attachment generation may reuse that PortID.

The Linux adapter performs range validation before the detach sysfs write. Only
an `EINVAL` returned by that specific write is classified as the canonical
not-bound/already-free sentinel, while the original errno remains wrapped and
unrelated `EINVAL` or `EIO` failures retain their generic classification.

The public `ListPorts` method remains a raw normalized kernel view and may
include free capacity rows. CLI `port` output and detach completion instead
filter only `Null` and `Available` rows so their user-facing view is limited to
claimed or active Ports. `NotAssigned` remains visible because the kernel has
claimed that vdev even though it has not assigned a local USB address yet.
These are behavioral corrections without a public Go signature change.

Linux also emits a synthetic `status_show_not_ready` row while a VHCI
controller lacks driver data. Its status and zero-valued identity resemble
`NotAssigned`, but its socket field contains sixteen zeroes rather than the
ordinary six-digit socket field. The adapter matches only that exact synthetic
shape and rejects the whole snapshot. `ListPorts` returns an error and no
partial Ports, and allocation propagates the same error rather than claiming
capacity or converting it to `ErrNoFreePort`. Genuine six-digit-socket
`NotAssigned` rows continue through the normalized public and CLI views as
claimed Ports.

### Run exactly three complete bidirectional ACM cycles

Three is the smallest bounded count that exercises more than one reattachment.
The exporter server and gadget remain available while the importer completes
exactly three sequential attachment generations. Every cycle derives two
deterministic payloads from its cycle index and direction so no earlier payload
can satisfy a later assertion.

After attach returns the exact numeric Port, the exporter writes its cycle
payload through `/dev/ttyGS*` and the importer reads the matching `/dev/ttyACM*`
bytes. The importer then sends the reverse payload and the exporter reads it.
Both byte counts and contents must match exactly before detach begins.

Detach targets only the returned Port. Before the next attach, the runner fails
closed unless that Port is absent or free, the importer ACM device has
disappeared, the exporter session has drained, the production server remains
healthy, and the exported gadget is ready for another connection. A timeout,
ambiguous device selection, stale Port, or kernel error fails the test rather
than degrading to a warning or skip.

The focused single-kernel drain uses stricter raw evidence. Only an
`fs.ErrNotExist` result proves the prior block device disappeared; permission or
other stat failures keep polling. The VHCI status snapshot must be nonempty,
structurally valid, and contain only fully zeroed `Null` rows. Exact Port release
accepts only a missing row, `Null`, or `Available`; `NotAssigned` remains
claimed. Explicit gadget release returns its teardown error so a later cycle
cannot reuse a UDC after silent cleanup failure.

### Delay both guest egress paths with netem

Before USB/IP discovery or attachment, each guest installs an exact 25 ms
`tc netem delay` qdisc on its dedicated inter-guest interface. Importer egress
therefore delays importer-to-exporter USB/IP traffic and exporter egress delays
the reverse direction. The runner records the effective qdisc configuration and
captures directional qdisc byte and packet counters before and after the three
cycles.

Success requires byte and packet counter advancement on both qdiscs plus exact bidirectional
ACM payload equality. This verifies real delayed inter-guest traffic without a
flaky wall-clock assertion. The single-kernel proxy remains useful for direct
post-handoff byte accounting, while the two-guest case establishes that the
production network and kernel topology survives the delay.

Host-global `tc` state is not used: impairment remains isolated inside the two
disposable guests. Delay alone must not be presented as evidence for loss,
jitter, reordering, bandwidth limiting, outage, or reconnect behavior.

### Make VM validation a narrow tracked manual exception

A dedicated visible Make target invokes one tracked Bazel local/manual target.
The Bazel target declares repository scripts and fixtures, but the final runner
is an explicit non-hermetic exception because it must access `/dev/kvm`, invoke
host QEMU and SSH tooling, acquire or reuse a checksum-pinned guest image, and
create test networking. Missing KVM, QEMU, image integrity, network access, guest
modules, writable configfs, or required tools fails with an actionable error; it
does not silently skip.

The image URL, release identity, and cryptographic checksum are repository
inputs. Downloads populate repository-local persistent cache state and occur
only when the verified image is absent. The runner builds the production binary
once through Bazel before booting final guests and does not compile inside them.

Each guest is fixed at one vCPU and 1024 MiB, for a two-vCPU/2048-MiB aggregate
final topology. Provisioning that can be performed sequentially remains
sequential. Build concurrency and memory are bounded separately so VM and Bazel
peaks do not overlap unexpectedly.

The target is excluded from default, pull-request, nightly, and release GitHub
workflows. It is deliberate manual maintainer evidence for a capable Linux KVM
host, not evidence supplied by standard hosted automation.

### Give failures and cleanup explicit ownership

The host runner owns guest processes, overlays, SSH forwarding, logs, and the
overall deadline. The exporter role owns its qdisc, gadget, bind, server, and
ACM reader/writer. The importer role owns its qdisc, attachment, Port, and ACM
reader/writer. Cleanup runs in reverse acquisition order and attempts exact-Port
detach before stopping the exporter or guests.

Failure preserves bounded diagnostic logs while terminating readers, server,
guest processes, and temporary connection state. Success also verifies that no
skip or prerequisite marker, USB/IP sequence error, kernel oops, BUG, or panic
appears in either guest's captured output.

Success scanning begins only while both QEMU roles are still confirmed alive
and after every required capture command succeeds. Kernel version, bounded
kernel log, journal, system/process state, and role-specific JSON evidence must
all exist and be nonempty for both roles before the combined log scan can pass.
Best-effort diagnostics remain available for failure cleanup but never count as
success evidence.

Cleanup removes the run root and its guest overlays only after shutdown logic
confirms every role stopped and no role still owns its overlay. If any guest
cannot be confirmed stopped, the run root is preserved for safety and diagnosis
rather than deleting an image that a live QEMU process may still be using.

## Risks / Trade-offs

- **Two guests can exhaust the host** → Fix each guest at one vCPU and 1024 MiB,
  provision sequentially, serialize the build, and avoid overlapping heavy
  validation.
- **External image or tool state is non-hermetic** → Pin image identity and
  checksum, declare every host prerequisite, isolate caches below repository
  state, and keep the workflow manual and narrowly tagged.
- **Guest device enumeration is asynchronous** → Use bounded polling of exact
  Port, sysfs, device identity, server, and session signals; fail closed on
  ambiguity or timeout.
- **ACM reads can deadlock cleanup** → Use supervised bounded readers, exact
  byte counts, per-operation deadlines, and process ownership recorded before
  writers start.
- **Netem configuration could be present but unused** → Require both qdiscs to
  report exactly 25 ms of delay and directional byte and packet counter growth.
- **Delay coverage may be misread as general adverse-network coverage** → Keep
  the excluded impairment classes explicit in proposal, specs, documentation,
  and traceability.
