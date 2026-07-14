// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

// PortID identifies a vhci port.
type PortID uint32

// Port describes an attached vhci port.
type Port struct {
	// ID is the numeric vhci port identifier.
	ID PortID
	// Status is the port state (available/used/error/...).
	Status Status
	// Speed is the negotiated USB speed.
	Speed Speed
	// DeviceID encodes (busnum << 16) | devnum of the remote device.
	DeviceID DeviceID
	// Remote is the peer address serving this port. It is zero when the
	// attachment is known only from kernel state because VHCI does not retain
	// exporter endpoint metadata.
	Remote RemoteEndpoint
	// BusID is the remote busid as reported by the exporter. It is empty when
	// the attachment is known only from kernel state.
	BusID BusID
	// LocalBusID is the local representation of the busid if one exists;
	// empty for ports without a local sysfs entry.
	LocalBusID BusID
}
