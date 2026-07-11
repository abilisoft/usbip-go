## Purpose

Specify the Linux kernel adapter behavior for sysfs, netlink events, fd handoff, device bind/unbind, port status, module probing, and errno classification.

## Requirements

### Requirement: Kernel adapters are role-specific

The kernel layer SHALL expose importer, exporter, and events adapters with shared substrate but role-specific module expectations and method sets.

#### Scenario: Importer adapter probes modules

- **WHEN** importer-side kernel operations run
- **THEN** the adapter requires `usbip_core` and `vhci_hcd`
- **AND** it does not require `usbip_host`

#### Scenario: Exporter adapter probes modules

- **WHEN** exporter-side kernel operations run
- **THEN** the adapter requires `usbip_core` and `usbip_host`
- **AND** it does not require `vhci_hcd`

### Requirement: AttachRemote preserves fd handoff ordering

Importer attach SHALL write the duplicated socket fd to `vhci_hcd` sysfs before closing the userspace connection reference.

#### Scenario: Kernel attach succeeds

- **WHEN** the sysfs attach write returns success
- **THEN** the kernel owns its socket reference
- **AND** the adapter closes the userspace connection reference after the write

#### Scenario: Kernel attach fails before handoff

- **WHEN** attach fails before the sysfs write succeeds
- **THEN** the adapter leaves the caller-owned connection open for the caller to close

### Requirement: AttachRemote validates flat port bounds before sysfs write

The adapter SHALL validate the selected flat vhci Port ID against discovered status topology before writing to the attach sysfs file.

#### Scenario: Port ID is out of range

- **WHEN** a requested Port ID is outside `[0, NControllers*VHCIPorts)`
- **THEN** the adapter returns a diagnostic error before the kernel sysfs write

### Requirement: VHCI status parsing is defensive

The adapter SHALL parse `status` and `status.N` files using validated topology, skip malformed rows with a warning, and fail on controller-window inconsistencies.

#### Scenario: Status row is malformed

- **WHEN** a status row has the wrong shape or invalid values
- **THEN** the row is skipped and a warning is logged

#### Scenario: Status row belongs to the wrong controller file

- **WHEN** a parsed flat port falls outside the controller file's valid window
- **THEN** the status read fails because kernel state is inconsistent

### Requirement: Bind follows upstream-safe ordering

Bind SHALL make a local Device exportable by adding the BusID to `match_busid`, unbinding the current device-level driver, then writing the BusID to `usbip-host/bind` as a fallback.

#### Scenario: Device auto-probe races are possible

- **WHEN** Bind begins
- **THEN** `match_busid` is populated before unbinding the current driver so `usbip_host` can win the kernel auto-probe race

#### Scenario: Final bind write fails

- **WHEN** the `usbip-host/bind` fallback write fails
- **THEN** the adapter removes the BusID from `match_busid`
- **AND** it best-effort probes drivers to restore the original binding

### Requirement: Bind protects unsupported and dangerous devices

Bind SHALL perform non-mutating preflight checks for module availability, VHCI loop prevention, hub rejection, and already-exported state before sysfs mutation.

#### Scenario: Device is a hub

- **WHEN** a Device has hub class
- **THEN** Bind returns `ErrUnsupportedDevice`
- **AND** it does not detach the hub's driver

#### Scenario: Device is already exported

- **WHEN** the bare Device driver is already `usbip-host`
- **THEN** Bind returns `ErrDeviceAlreadyBound` before further sysfs mutation

### Requirement: Bind handles network-device refcounts

Before unbinding USB network devices, the adapter SHALL attempt to deactivate associated netdevs so kernel refcounts can drain.

#### Scenario: USB network interface is up

- **WHEN** Bind discovers associated netdevs
- **THEN** it attempts to clear `IFF_UP` before the bare-device unbind

### Requirement: Unbind performs pre-disconnect and rebind cleanup

Unbind SHALL verify the Device is bound to `usbip-host`, best-effort disconnect any active importer session, unbind from `usbip-host`, remove `match_busid`, and trigger default driver probing.

#### Scenario: Active importer session exists

- **WHEN** Unbind runs against an actively imported Device
- **THEN** it writes `-1` to `usbip_sockfd` before the unbind sequence to avoid hanging on in-flight URBs

#### Scenario: Device is not bound to usbip-host

- **WHEN** Unbind sees no driver or a non-`usbip-host` driver
- **THEN** it returns `ErrDeviceNotBound`

### Requirement: ExportOnConn and Disconnect own exporter-side fd handoff

Exporter fd handoff SHALL write the duplicated accepted socket fd to the Device's `usbip_sockfd`, while Disconnect writes `-1` to drop the kernel session.

#### Scenario: Export handoff succeeds

- **WHEN** `ExportOnConn` writes a duplicated fd successfully
- **THEN** the caller still closes its userspace connection reference after the handler completes

#### Scenario: Shutdown disconnects a session

- **WHEN** exporter Shutdown needs to end a kernel-owned session
- **THEN** the adapter writes `-1` to that Device's `usbip_sockfd`

### Requirement: Netlink event fan-out is shared and non-blocking

EventsAdapter SHALL open one `NETLINK_KOBJECT_UEVENT` socket lazily, fan events out to subscribers, and tear down the dispatcher when the last subscriber unsubscribes.

#### Scenario: First subscriber starts

- **WHEN** the first Subscribe call succeeds
- **THEN** the netlink socket opens and the dispatcher starts after that subscriber is registered

#### Scenario: Subscriber is slow

- **WHEN** a subscriber channel is full
- **THEN** events for that subscriber may be dropped with a warning rather than blocking all subscribers

### Requirement: Event mapping separates exporter and importer concerns

The events adapter SHALL deliver usbip-host bind/unbind events even on exporter-only hosts and SHALL load VHCI topology lazily only for VHCI-shaped events.

#### Scenario: Exporter-only host subscribes

- **WHEN** `vhci_hcd` is absent but usbip-host events are available
- **THEN** Subscribe still succeeds and can deliver device bound/unbound events

### Requirement: Kernel errors map to public sentinels

Kernel adapter errors SHALL classify common sysfs and errno failures into domain sentinels such as permission, not found, already bound, not bound, no free port, unsupported device, and missing module.

#### Scenario: Permission errno occurs

- **WHEN** a sysfs operation returns EACCES or EPERM
- **THEN** the returned error matches `ErrPermission`

#### Scenario: Module disappears at runtime

- **WHEN** a required `/sys/module` entry is absent before an operation
- **THEN** the returned error matches `ErrKernelModuleMissing`
