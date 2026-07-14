## MODIFIED Requirements

### Requirement: Port view has stable field names

Port JSON views SHALL include `id`, `status`, `speed`, `remote`, `busid`, and `local_busid`. `busid` and `remote` SHALL describe exporter-side identity only when known; they SHALL NOT be populated from importer-local VHCI topology.

#### Scenario: Local BusID is unknown

- **WHEN** the kernel has not mapped a local BusID for a Port
- **THEN** `local_busid` is the empty string instead of being omitted

#### Scenario: Remote attachment metadata is unavailable

- **WHEN** a fresh Importer or CLI process lists a kernel-owned attachment without matching process-local metadata
- **THEN** `remote` and `busid` are empty strings instead of being omitted
- **AND** `remote` is not rendered as `:3240`
- **AND** `local_busid` retains the importer-local sysfs identity
