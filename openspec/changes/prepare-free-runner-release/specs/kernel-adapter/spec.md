## MODIFIED Requirements

### Requirement: Event mapping separates exporter and importer concerns

The events adapter SHALL deliver usbip-host bind/unbind events even on
exporter-only hosts and SHALL load VHCI topology lazily only for VHCI-shaped
events. It SHALL classify the Linux driver-core USB event shape rather than
depending on a synthetic usbip-host subsystem.

#### Scenario: Exporter-only host subscribes

- **WHEN** `vhci_hcd` is absent but usbip-host events are available
- **THEN** Subscribe still succeeds and can deliver device bound/unbound events

#### Scenario: Driver core reports usbip-host lifecycle

- **WHEN** Linux emits `SUBSYSTEM=usb ACTION=bind DRIVER=usbip-host` for a device
- **THEN** the adapter emits a device-bound event and remembers that bus ID
- **AND** a matching `ACTION=unbind` without a `DRIVER` field emits a device-unbound event
- **AND** unrelated USB driver unbinds do not emit usbip-host lifecycle events
