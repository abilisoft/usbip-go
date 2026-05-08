// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"strconv"
	"time"
)

// EventKind tags the concrete type of a domain event. Values are used
// as the JSON "kind" discriminator in §7.5 event streams.
type EventKind uint8

// Event kinds. Canonical string forms are returned by EventKind.String
// and used as JSON discriminators.
const (
	EventPortAttached EventKind = iota
	EventPortDetached
	EventPortErrored
	EventDeviceBound
	EventDeviceUnbound
	EventRemoteDeviceAdded
	EventRemoteDeviceRemoved
	EventSessionStarted
	EventSessionEnded
)

// eventKindNames is indexed by EventKind value; an entry is present iff
// the index < len(eventKindNames). Using a slice avoids the cyclomatic
// complexity of a long switch and keeps the String implementation O(1).
func eventKindNames() []string {
	return []string{
		"port_attached",
		"port_detached",
		"port_errored",
		"device_bound",
		"device_unbound",
		"remote_device_added",
		"remote_device_removed",
		"session_started",
		"session_ended",
	}
}

// String returns the canonical snake_case kind discriminator.
// Unknown values return "event(N)" with N in decimal.
func (k EventKind) String() string {
	names := eventKindNames()
	if int(k) < len(names) {
		return names[k]
	}

	return "event(" + strconv.FormatUint(uint64(k), 10) + ")"
}

// Event is the closed polymorphic union of domain events. Each concrete
// event type has a fixed EventKind() returning its discriminator.
type Event interface {
	EventKind() EventKind
}

// PortAttachedEvent is emitted when a remote device is successfully attached.
type PortAttachedEvent struct {
	At   time.Time
	Port Port
}

// EventKind implements Event.
func (PortAttachedEvent) EventKind() EventKind { return EventPortAttached }

// PortDetachedEvent is emitted when a previously-attached port is released.
// Reason is a free-form human-readable explanation.
type PortDetachedEvent struct {
	At     time.Time
	Port   Port
	Reason string
}

// EventKind implements Event.
func (PortDetachedEvent) EventKind() EventKind { return EventPortDetached }

// PortErroredEvent is emitted when the vhci port transitions to the
// error state. Err is the error message captured at the transition.
type PortErroredEvent struct {
	At   time.Time
	Port Port
	Err  string
}

// EventKind implements Event.
func (PortErroredEvent) EventKind() EventKind { return EventPortErrored }

// DeviceBoundEvent is emitted when a local device becomes exportable
// (bound to usbip-host).
type DeviceBoundEvent struct {
	At     time.Time
	Device Device
}

// EventKind implements Event.
func (DeviceBoundEvent) EventKind() EventKind { return EventDeviceBound }

// DeviceUnboundEvent is emitted when a local device is unbound from
// usbip-host and returned to its original driver.
type DeviceUnboundEvent struct {
	At     time.Time
	Device Device
}

// EventKind implements Event.
func (DeviceUnboundEvent) EventKind() EventKind { return EventDeviceUnbound }

// RemoteDeviceAddedEvent is emitted when a new device appears on a
// monitored remote peer.
type RemoteDeviceAddedEvent struct {
	At     time.Time
	Remote RemoteEndpoint
	Device Device
}

// EventKind implements Event.
func (RemoteDeviceAddedEvent) EventKind() EventKind { return EventRemoteDeviceAdded }

// RemoteDeviceRemovedEvent is emitted when a previously-seen device
// disappears from a monitored remote peer. Only BusID is known
// because the device is no longer enumerable.
type RemoteDeviceRemovedEvent struct {
	At     time.Time
	Remote RemoteEndpoint
	BusID  BusID
}

// EventKind implements Event.
func (RemoteDeviceRemovedEvent) EventKind() EventKind { return EventRemoteDeviceRemoved }

// SessionStartedEvent is emitted when a client completes the USBIP
// handshake and is assigned a Session.
type SessionStartedEvent struct {
	At      time.Time
	Session Session
}

// EventKind implements Event.
func (SessionStartedEvent) EventKind() EventKind { return EventSessionStarted }

// SessionEndedEvent is emitted when a Session closes, for any reason.
type SessionEndedEvent struct {
	At      time.Time
	Session Session
	Reason  string
}

// EventKind implements Event.
func (SessionEndedEvent) EventKind() EventKind { return EventSessionEnded }
