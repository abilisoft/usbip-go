## ADDED Requirements

### Requirement: VHCI port identity remains local at the kernel boundary

The kernel adapter SHALL map VHCI `local_busid` only to Port.LocalBusID. It SHALL leave remote Port.BusID and Port.Remote unknown because Linux VHCI status and events do not retain exporter identity.

#### Scenario: ListPorts parses an attached VHCI row

- **WHEN** VHCI status reports local_busid `2-1`
- **THEN** the adapter returns LocalBusID `2-1`
- **AND** BusID and Remote are empty

#### Scenario: VHCI event is mapped

- **WHEN** a VHCI attach, detach, or error event resolves to a local kernel row
- **THEN** the emitted Port carries the local busid only in LocalBusID
- **AND** it does not present that value as the remote BusID

### Requirement: Detach reconciles state inside the mutation boundary

ImporterAdapter DetachPort SHALL inspect fresh VHCI status and perform the detach write while holding the same port-mutation boundary used by attach.

#### Scenario: Requested Port is attached

- **WHEN** the validated PortID identifies a non-free VHCI row
- **THEN** DetachPort writes that PortID to the detach sysfs attribute

#### Scenario: Requested Port is absent or free

- **WHEN** the PortID is outside the live rows or its status is null or available
- **THEN** DetachPort returns the canonical not-bound sentinel
- **AND** it does not write the detach sysfs attribute

## MODIFIED Requirements

### Requirement: Event mapping separates exporter and importer concerns

The events adapter SHALL deliver `usbip-host` bind/unbind events even on exporter-only hosts. For each VHCI-shaped event, it SHALL discover fresh VHCI topology and validate controller/root-Port coordinates; if either step fails, it SHALL make at most one complete retry for that same event using a newly rediscovered topology before dropping it. It SHALL retain no topology or retry state across events, and non-VHCI exporter events SHALL bypass VHCI topology discovery.

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
- **THEN** the mapper discovers topology and validates controller/root-Port coordinates for that event, makes at most one complete retry with a newly rediscovered topology if either step fails, emits the mapped event if either attempt succeeds, drops only that event if both attempts fail, and retains no topology or retry state for a later event
