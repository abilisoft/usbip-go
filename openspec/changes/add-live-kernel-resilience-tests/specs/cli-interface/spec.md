## MODIFIED Requirements

### Requirement: detach and port operate on numeric port IDs

`usbip-go detach <port>` SHALL detach a kernel-owned Port by ID even when the
attachment was created by an earlier CLI process. `usbip-go port` and
`usbip-go port --id N` SHALL report only active Ports; normalized `Null` and
`Available` kernel-capacity rows SHALL remain excluded. An ordinary
six-digit-socket `NotAssigned` row SHALL remain visible because it represents a
claimed vdev awaiting its USB address. Linux's exact sixteen-zero
controller-not-ready placeholder SHALL fail below the CLI and SHALL NOT be
rendered as a Port.

#### Scenario: Port filter misses

- **WHEN** `usbip-go port --id N` is supplied for a non-attached or free Port
- **THEN** the command returns a not-found classified error

#### Scenario: Independent attach and detach processes operate on one Port

- **WHEN** `usbip-go attach` hands a connection to the kernel and exits
- **AND** a later independent `usbip-go detach <port>` process targets the returned Port
- **THEN** detach performs the authoritative kernel mutation and succeeds
- **AND** the Port no longer appears in the CLI active-Port view

#### Scenario: Kernel capacity and claimed unassigned rows are classified

- **WHEN** the kernel adapter reports `Null`, ordinary claimed `NotAssigned`, or `Available` Port rows
- **THEN** `usbip-go port` and detach completion omit `Null` and `Available`
- **AND** claimed `NotAssigned`, active `Used`, `Error`, or future non-free statuses remain visible
- **AND** a controller-not-ready placeholder produces an error and no synthetic CLI Ports
