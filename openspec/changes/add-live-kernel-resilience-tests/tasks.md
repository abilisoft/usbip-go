## 1. Focused Single-Kernel Prerequisites

- [x] 1.1 Refactor the live URB harness to reuse one Importer safely, register attachment cleanup immediately, and make detach/drain failures fail closed.
- [x] 1.2 Replace the two-generation reattach case with exactly three sequential fresh-gadget cycles using distinct deterministic payloads, exact-Port detach, kernel drain, and explicit gadget release.
- [x] 1.3 Implement and directly test a race-safe single-session bidirectional TCP forwarder with 20 ms per-chunk delay, directional metrics, idempotent close, and bounded goroutine drain.
- [x] 1.4 Add a single-kernel live integration case that proves exact payload transfer and post-handoff URB traffic in both directions through the delayed forwarder, followed by exact-Port detach and drain.
- [x] 1.5 Make abrupt daemon restart coverage wait for stable handoff, reconcile the retained exporter kernel session before exact client-Port release, reject `NotAssigned` as free, and prove a fresh replacement attachment.
- [x] 1.6 Make single-kernel drain evidence fail closed on non-`ErrNotExist` block stat results, empty or malformed VHCI snapshots, claimed `NotAssigned` Ports, and gadget cleanup errors.

## 2. Tracked Two-Guest KVM Resilience Test

- [x] 2.1 Move the ignored two-guest prototype into repository-owned source and expose it through a dedicated visible Make entrypoint backed by a tracked Bazel local/manual test target.
- [x] 2.2 Pin and verify the guest image and declare the narrow KVM, QEMU, SSH, image-network, and inter-guest-network prerequisites without adding the workflow to GitHub-hosted automation.
- [x] 2.3 Cap exporter and importer guests at one vCPU and 1024 MiB each, keep provisioning and production-binary build peaks serialized, and enforce bounded host/guest shutdown.
- [x] 2.4 Provision distinct exporter and importer kernel roles, create a real ACM gadget behind `dummy_hcd`, and exercise the production bind, serve, list, attach, port, and detach paths across a dedicated direct inter-guest network.
- [x] 2.5 Run exactly three sequential cycles with unique deterministic exporter-to-importer and importer-to-exporter ACM payloads, exact byte comparison, and detach of the exact returned Port.
- [x] 2.6 Fail closed between cycles until importer Port/device state and exporter session/device readiness have drained, rejecting skips, ambiguous devices, stale state, kernel USB/IP sequence errors, oopses, BUGs, and panics.
- [x] 2.7 Apply exactly 25 ms of `tc netem` delay to both dedicated guest egress interfaces and require the effective qdisc configuration plus directional byte/packet-counter advancement during the bidirectional payload cycles.
- [x] 2.8 Add focused tests for runner argument validation, image verification, resource caps, cycle accounting, qdisc verification, failure classification, and cleanup behavior without booting VMs.
- [x] 2.9 Let a fresh Importer detach a kernel-owned Port, share overlapping untracked attempts, permit retry after failure, block same-Port attach reservation, and enroll the mutation in Close lifecycle waiting.
- [x] 2.10 Scope detach-write `EINVAL` classification to the canonical already-free/not-bound sentinel while preserving other kernel error classifications.
- [x] 2.11 Keep public `ListPorts` as the raw kernel-capacity view while CLI Port output and detach completion exclude all free Port states.
- [x] 2.12 Add focused regression and race tests for tracked and untracked detach success, overlap, cancellation, failure, retry, Close, stale-handle removal, reservation conflict, CLI filtering, adapter error mapping, and public facade forwarding.
- [x] 2.13 Reject Linux's exact sixteen-zero controller-not-ready VHCI placeholder from listing and allocation while preserving ordinary six-digit-socket `NotAssigned` rows as claimed Ports.
- [x] 2.14 Require both guests alive and successful nonempty kernel, journal, system, and role evidence before success scanning, and preserve run roots and overlays unless every guest is confirmed stopped.

## 3. Specifications, Documentation, and Traceability

- [x] 3.1 Rescope the active proposal, design, delta specs, and tasks so the single-kernel Go tests are prerequisites and the tracked two-guest workflow is the completion target.
- [x] 3.2 Synchronize the accepted cli-interface, developer-workflow, exporter-daemon, importer-lifecycle, kernel-adapter, public-library-api, release-packaging, security-release-quality, and transport-networking requirements into the main OpenSpec specs after implementation matches the deltas.
- [x] 3.3 Update `openspec/TRACEABILITY.md` totals, active-change inventory, shifted citations, and precise evidence for both focused and two-guest tests after final code line numbers stabilize.
- [x] 3.4 Document the dedicated Make command, KVM/QEMU and network prerequisites, checksum-pinned image cache, fixed guest resource limits, netem semantics, abrupt daemon-restart reconciliation, VHCI placeholder distinction, strict success evidence, overlay preservation, non-goals, and fail-closed diagnostics.

## 4. Validation and Publication

- [x] 4.1 Run fresh formatting, focused Go/proxy race tests, runner unit tests, lint, and strict OpenSpec change/spec validation after the final edit.
- [x] 4.2 Run the focused repeated-cycle and delayed-path single-kernel cases in the memory-capped KVM environment and confirm neither skips.
- [x] 4.3 Run the tracked two-guest target and prove exactly three non-skipped bidirectional cycles, both netem qdisc counter deltas, exact-Port/device/session drain between cycles, and clean guest kernel diagnostics.
- [x] 4.4 Run the complete memory-capped single-kernel integration suite and the repository's required local CI, coverage, and patch-coverage gates without overlapping heavy VM/build workloads.
- [x] 4.5 Create and verify signed Conventional Commits with no attribution trailers, publish the branch, and confirm the pull-request checks pass while disclosing the separate manual two-guest evidence.
