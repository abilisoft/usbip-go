## ADDED Requirements

### Requirement: VHCI topology snapshots are fresh and operation-local

The kernel adapter SHALL rediscover VHCI topology for each importer operation and each relevant VHCI-shaped event, SHALL use one status-topology snapshot throughout a single AttachRemote allocation and bounds-validation sequence, and SHALL validate event controller/root-Port coordinates against the fresh topology before flat-Port conversion.

#### Scenario: VHCI module reload changes topology

- **WHEN** a long-lived adapter performs an operation after `vhci_hcd` is unloaded and reloaded with different controllers, ports, or bus mappings
- **THEN** the operation uses the newly discovered topology rather than a previously successful snapshot

#### Scenario: Attach selects and validates a Port

- **WHEN** AttachRemote reads status rows, selects a free Port, and validates the Port before the sysfs write
- **THEN** all three steps use one internally consistent status-topology snapshot

#### Scenario: Consecutive VHCI events span a reload

- **WHEN** two relevant VHCI-shaped events are mapped on opposite sides of a module reload
- **THEN** each event is mapped only when its controller and root Port agree with topology discovered for that event

#### Scenario: Delayed event names a previous controller

- **WHEN** an event's controller suffix disagrees with the fresh BusMap location after reload
- **THEN** the stale event is dropped instead of remapped to a different flat Port

#### Scenario: Event root Port exceeds the fresh hub width

- **WHEN** an event's 1-indexed root Port is zero or greater than HCPorts
- **THEN** the malformed event is dropped before FlatPort arithmetic

#### Scenario: Exporter-only event is mapped

- **WHEN** a usbip-host driver event is mapped on a host without `vhci_hcd`
- **THEN** the event bypasses VHCI topology discovery

## MODIFIED Requirements

### Requirement: AttachRemote validates flat port bounds before sysfs write

The adapter SHALL validate the selected flat vhci Port ID against the same freshly discovered status topology used to select it before writing to the attach sysfs file.

#### Scenario: Port ID is out of range

- **WHEN** a requested Port ID is outside `[0, NControllers*VHCIPorts)`
- **THEN** the adapter returns a diagnostic error before the kernel sysfs write

#### Scenario: Topology changes during an AttachRemote call

- **WHEN** the live VHCI topology changes after AttachRemote has captured its operation-local snapshot
- **THEN** selection and pre-write validation remain internally consistent with that snapshot

### Requirement: VHCI status parsing is defensive

The adapter SHALL parse `status` and `status.N` files using one freshly discovered validated status topology per operation, skip malformed rows with a warning, and fail on controller-window inconsistencies.

#### Scenario: Status row is malformed

- **WHEN** a status row has the wrong shape or invalid values
- **THEN** the row is skipped and a warning is logged

#### Scenario: Status row belongs to the wrong controller file

- **WHEN** a parsed flat port falls outside the controller file's valid window
- **THEN** the status read fails because kernel state is inconsistent

### Requirement: Event mapping separates exporter and importer concerns

The events adapter SHALL deliver usbip-host bind/unbind events even on exporter-only hosts and SHALL discover fresh VHCI topology only for each VHCI-shaped event.

#### Scenario: Exporter-only host subscribes

- **WHEN** `vhci_hcd` is absent but usbip-host events are available
- **THEN** Subscribe still succeeds and can deliver device bound/unbound events

#### Scenario: Driver core reports usbip-host lifecycle

- **WHEN** Linux emits `SUBSYSTEM=usb ACTION=bind DRIVER=usbip-host` for a device
- **THEN** the adapter emits a device-bound event and remembers that bus ID
- **AND** a matching `ACTION=unbind` without a `DRIVER` field emits a device-unbound event
- **AND** unrelated USB driver unbinds do not emit usbip-host lifecycle events

#### Scenario: VHCI event arrives

- **WHEN** an event carries a VHCI-shaped devpath
- **THEN** the mapper discovers topology for that event and drops only that event if discovery or coordinate validation fails
