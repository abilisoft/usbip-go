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
	EventPortReconnectExhausted
	EventDeviceBound
	EventDeviceUnbound
	EventSessionStarted
	EventSessionEnded
)

// String returns the canonical snake_case kind discriminator.
// Unknown values return "event(N)" with N in decimal.
func (k EventKind) String() string {
	switch k {
	case EventPortAttached:
		return "port_attached"
	case EventPortDetached:
		return "port_detached"
	case EventPortErrored:
		return "port_errored"
	case EventPortReconnectExhausted:
		return "port_reconnect_exhausted"
	case EventDeviceBound:
		return "device_bound"
	case EventDeviceUnbound:
		return "device_unbound"
	case EventSessionStarted:
		return "session_started"
	case EventSessionEnded:
		return "session_ended"
	default:
		return "event(" + strconv.FormatUint(uint64(k), 10) + ")"
	}
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

// PortReconnectExhaustedEvent is emitted by the importer's reconnect
// watcher when MaxAttempts has been reached without a successful reattach.
// Port is a snapshot of the last successful Attach (the kernel slot is
// already gone at emission time, so this captures what was true while the
// port was viable). Attempts is the number of reconnect attempts actually
// made (not MaxAttempts). LastError is the stringified final attempt
// error; the domain layer does not carry Go error values across the
// JSON boundary.
//
// LastError is diagnostic free-form text. Wrapping by the importer and
// kernel adapter typically embeds the peer endpoint, the BusID, and
// absolute sysfs paths. Downstream JSON consumers MUST treat this
// field as untrusted display text — do not parse it for control flow,
// and consider scrubbing it before forwarding to a less-privileged
// audience that should not see operator-level diagnostic strings.
type PortReconnectExhaustedEvent struct {
	At        time.Time
	Port      Port
	Attempts  int
	LastError string
}

// EventKind implements Event.
func (PortReconnectExhaustedEvent) EventKind() EventKind { return EventPortReconnectExhausted }

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
