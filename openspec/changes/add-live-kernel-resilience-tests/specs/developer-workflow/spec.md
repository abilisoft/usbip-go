## ADDED Requirements

### Requirement: Two-guest KVM resilience validation is an explicit manual workflow

The repository SHALL expose production USB/IP validation across distinct
exporter and importer guest kernels through `make test-integration-vm`, backed
by one tracked Bazel local/manual/external target. The target SHALL declare
repository-owned scripts and fixtures while narrowly identifying KVM, QEMU,
cloud-image and SSH tooling, checksum-pinned image acquisition, and direct
inter-guest networking as non-hermetic host prerequisites. It SHALL cap each
guest at one vCPU and 1024 MiB, bound Bazel to one job, one CPU, 1024 MiB of
scheduled memory, and a 512 MiB host JVM, disable test-result caching, enforce
an overall deadline, and SHALL NOT run on standard GitHub-hosted automation.

#### Scenario: Two-guest KVM integration runs explicitly

- **WHEN** `make test-integration-vm` runs on a capable Linux KVM host
- **THEN** Bazel executes one tracked local/manual/external two-guest target with test-result caching disabled and an overall deadline
- **AND** the target builds the production binary through Bazel before booting distinct exporter and importer guests
- **AND** each guest uses one vCPU and 1024 MiB while Bazel uses one job, one scheduled CPU, 1024 MiB of scheduled memory, and a 512 MiB host JVM
- **AND** a dedicated direct inter-guest interface carries USB/IP while separate user-mode interfaces carry host SSH
- **AND** missing host tools, unusable KVM, image checksum failure, unavailable network acquisition, guest prerequisite failure, or a skipped test causes a non-zero result
- **AND** success requires both guest roles still running plus successful nonempty kernel, journal, system, and role-state evidence before log scanning
- **AND** run state and guest overlays are removed only after every guest is confirmed stopped and are preserved otherwise

#### Scenario: Standard hosted automation runs

- **WHEN** default, pull-request, nightly, or release automation runs on standard GitHub-hosted runners
- **THEN** it does not schedule the local/manual two-guest KVM target
- **AND** the absence of KVM or privileged guest-kernel facilities is not misreported as passing two-guest evidence
