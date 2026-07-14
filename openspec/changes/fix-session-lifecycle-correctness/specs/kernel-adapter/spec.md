## ADDED Requirements

### Requirement: Exporter session activity is observable from sysfs

The exporter kernel adapter SHALL report whether a bound Device's `usbip_status` is actively used so the application can detect peer completion without relying on importer-side uevents.

#### Scenario: Export session is active

- **WHEN** `usbip_status` contains the kernel `SDEV_ST_USED` value
- **THEN** the adapter reports the exporter Session active

#### Scenario: Peer connection completed

- **WHEN** `usbip_status` contains an available or other non-used value
- **THEN** the adapter reports the exporter Session inactive

#### Scenario: Activity status cannot be read

- **WHEN** the per-device status attribute cannot be read or parsed
- **THEN** the adapter returns the read error instead of guessing that the Session ended

## MODIFIED Requirements

### Requirement: AttachRemote preserves fd handoff ordering

Importer attach SHALL reserve the selected local PortID with the application before writing the duplicated socket fd to `vhci_hcd` sysfs, then close the userspace connection reference only after the write succeeds. The adapter SHALL serialize attach and detach kernel mutations through one VHCI port-mutation boundary.

#### Scenario: Selected port reservation is rejected

- **WHEN** the application rejects the adapter-selected local PortID reservation
- **THEN** AttachRemote returns the reservation error before the sysfs attach write
- **AND** leaves the caller-owned connection open

#### Scenario: Kernel attach succeeds

- **WHEN** the sysfs attach write returns success
- **THEN** the kernel owns its socket reference
- **AND** the adapter closes the userspace connection reference after the write

#### Scenario: Kernel attach fails before handoff

- **WHEN** attach fails before the sysfs write succeeds
- **THEN** the adapter leaves the caller-owned connection open for the caller to close

#### Scenario: Attach and detach overlap

- **WHEN** AttachRemote and DetachPort are invoked concurrently on one adapter
- **THEN** their topology discovery, selected-port reservation, and sysfs mutations do not overlap
