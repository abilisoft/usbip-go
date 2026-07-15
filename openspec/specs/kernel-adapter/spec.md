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

### Requirement: AttachRemote validates flat port bounds before sysfs write

The adapter SHALL validate the selected flat vhci Port ID against the same freshly discovered status topology used to select it before writing to the attach sysfs file.

#### Scenario: Port ID is out of range

- **WHEN** a requested Port ID is outside `[0, NControllers*VHCIPorts)`
- **THEN** the adapter returns a diagnostic error before the kernel sysfs write

#### Scenario: Topology changes during an AttachRemote call

- **WHEN** the live VHCI topology changes after AttachRemote has captured its operation-local snapshot
- **THEN** selection and pre-write validation remain internally consistent with that snapshot

### Requirement: VHCI status parsing is defensive

The adapter SHALL parse `status` and `status.N` files using one freshly
discovered validated status topology per operation, skip malformed rows with a
warning, and fail on controller-window inconsistencies. It SHALL preserve an
ordinary `VDEV_ST_NOTASSIGNED` row whose socket field has the normal six-digit
width as a claimed Port. It SHALL reject Linux's exact
`status_show_not_ready` placeholder, identified by its sixteen-zero socket
field and otherwise zero not-assigned shape, so listing and allocation fail
closed without returning partial or synthetic Ports.

#### Scenario: Status row is malformed

- **WHEN** a status row has the wrong shape or invalid values
- **THEN** the row is skipped and a warning is logged

#### Scenario: Status row belongs to the wrong controller file

- **WHEN** a parsed flat port falls outside the controller file's valid window
- **THEN** the status read fails because kernel state is inconsistent

#### Scenario: Controller status is not ready

- **WHEN** a status snapshot contains Linux's exact sixteen-zero `status_show_not_ready` placeholder
- **THEN** the status read fails without returning partial or synthetic Ports
- **AND** `ListPorts` and free-Port allocation propagate the error instead of reporting claimed capacity or `ErrNoFreePort`
- **AND** an ordinary six-digit-socket `NotAssigned` row remains a claimed Port

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

### Requirement: Kernel errors map to public sentinels

Kernel adapter errors SHALL classify common sysfs and errno failures into
domain sentinels such as permission, not found, already bound, not bound, no
free port, unsupported device, and missing module. After Port range validation,
an `EINVAL` returned by the detach sysfs write SHALL classify as not bound while
preserving the underlying errno. This classification SHALL NOT apply to other
`EINVAL` paths or to `EIO`.

#### Scenario: Permission errno occurs

- **WHEN** a sysfs operation returns EACCES or EPERM
- **THEN** the returned error matches `ErrPermission`

#### Scenario: Module disappears at runtime

- **WHEN** a required `/sys/module` entry is absent before an operation
- **THEN** the returned error matches `ErrKernelModuleMissing`

#### Scenario: Detach write reports an already-free Port

- **WHEN** an in-range detach sysfs write returns `EINVAL`
- **THEN** the returned error matches `ErrDeviceNotBound`
- **AND** the returned error continues to wrap `EINVAL`

#### Scenario: Unrelated kernel operation returns EINVAL

- **WHEN** an operation other than the validated detach sysfs write returns `EINVAL`
- **THEN** the adapter does not classify that error as `ErrDeviceNotBound`
