## ADDED Requirements

### Requirement: Completed detach leaves the Importer reusable

The same Importer SHALL remain reusable after authoritative kernel state reports
a detached Port free, accepting a later kernel attachment without retaining the
prior attachment generation, payload, or Port ownership. An exact Port SHALL be free
only when absent, `Null`, or `Available`; `NotAssigned` remains claimed. Focused
single-kernel validation SHALL prove same-Importer reuse, and tracked two-guest
validation SHALL prove the kernel attachment lifecycle repeats safely across
independent short-lived production CLI Importers and distinct exporter and
importer kernel instances.

#### Scenario: Bounded single-kernel cycles repeat

- **WHEN** one Importer completes exactly three sequential Attach, cycle-specific deterministic byte-transfer, exact-Port Detach, and kernel-drain cycles against fresh VUDC gadgets
- **THEN** every cycle's bytes match its cycle-specific payload exactly
- **AND** block-device disappearance is accepted only when the filesystem reports `fs.ErrNotExist`
- **AND** VHCI drain requires a nonempty structurally valid snapshot whose rows are all `Null`
- **AND** the exact prior Port is absent, `Null`, or `Available`, never `NotAssigned`
- **AND** gadget release succeeds before the next cycle
- **AND** every later Attach produces a working data path

#### Scenario: Independent production lifecycle repeats across two guest kernels

- **WHEN** one importer guest uses independent short-lived CLI Importers to complete exactly three sequential production Attach, cycle-specific bidirectional ACM byte-transfer, exact-Port Detach, and fail-closed drain cycles against a production exporter in a distinct guest kernel
- **THEN** each exporter-to-importer and importer-to-exporter payload matches its cycle-specific bytes exactly
- **AND** the detached Port and imported ACM device drain before the next Attach
- **AND** the exporter session drains and the exported gadget remains ready for the next connection
- **AND** every later Attach produces a working inter-guest data path

## MODIFIED Requirements

### Requirement: Detach is idempotent for port teardown

`Detach` SHALL treat authoritative kernel Port ownership independently from
process-local attachment handles. A tracked detach SHALL cancel and drain its
reconnect watcher and share at most one kernel attempt per attachment
generation. A valid Port with no local handle SHALL use one shared untracked
attempt per Importer and PortID. Tracked handles SHALL be removed after success
or authoritative already-free classification only when the PortID still maps to
that exact handle. Failed untracked attempts SHALL clear their coordination
record so a later call can retry.

#### Scenario: Watcher is still running

- **WHEN** Detach is called while the reconnect watcher is active
- **THEN** the watcher is cancelled before kernel detach proceeds
- **AND** bounded waiting uses the AttachOptions ShutdownTimeout semantics

#### Scenario: Concurrent callers detach one tracked attachment

- **WHEN** multiple callers overlap while detaching the same tracked handle generation
- **THEN** exactly one caller issues the kernel detach
- **AND** other callers observe the same completed result

#### Scenario: Concurrent callers detach one untracked Port

- **WHEN** multiple callers on one fresh Importer overlap while detaching the same kernel-owned Port
- **THEN** exactly one caller issues the kernel detach
- **AND** other callers observe the same completed result

#### Scenario: Waiting detach caller is cancelled

- **WHEN** a follower's context is cancelled while the shared detach attempt continues
- **THEN** that follower returns its context error
- **AND** the owner and other followers continue observing the shared attempt

#### Scenario: Tracked kernel detach fails

- **WHEN** the shared tracked kernel detach attempt returns an error other than already-free
- **THEN** every overlapping caller observes that error
- **AND** the exact handle remains registered so a later call can retry

#### Scenario: Untracked kernel detach fails

- **WHEN** the shared untracked kernel detach attempt returns an error
- **THEN** every overlapping caller observes that error
- **AND** the coordination record is removed so a later call can retry

#### Scenario: PortID is reused before an old detach completes

- **WHEN** an old detach attempt completes after the PortID maps to a different attachment pointer
- **THEN** the old attempt does not remove the newer attachment's handle

#### Scenario: Tracked Port is already free

- **WHEN** authoritative kernel detach reports that a tracked Port is already free
- **THEN** the result is classified with the canonical not-bound/device sentinel
- **AND** the exact stale handle is removed so a later generation can reuse that PortID

#### Scenario: Fresh Importer detaches an untracked kernel Port

- **WHEN** a fresh Importer receives a valid PortID still owned by the kernel
- **THEN** it invokes the serialized kernel detach mutation without a `ListPorts` preflight

#### Scenario: Close overlaps an untracked detach

- **WHEN** `Close` begins while an untracked detach mutation is active
- **THEN** Close waits for that mutation subject to the configured lifecycle deadline

#### Scenario: Attach reservation overlaps an untracked detach

- **WHEN** attach attempts to reserve a PortID owned by an active untracked detach in the same Importer
- **THEN** reservation is rejected until the detach attempt completes
