package usbip

import "github.com/abilisoft/usbip-go/pkg/domain"

// Pure-data types are aliased to pkg/domain so consumers referencing
// usbip.Device and domain.Device get the same type. Aliasing instead of
// redeclaring means a value from one package drops into a parameter of
// the other without conversion. Spec §5.7.
type (
	// Device describes a USB device enumerated over USB/IP.
	Device = domain.Device

	// Port describes an attached vhci port on the importer side.
	Port = domain.Port

	// Session describes a single client connection from the daemon's view.
	Session = domain.Session

	// BusID is the stable USB topology identifier (e.g. "1-1.2").
	BusID = domain.BusID

	// Speed is the negotiated USB speed.
	Speed = domain.Speed

	// Status is a vhci port state (available/used/error/...).
	Status = domain.Status

	// RemoteEndpoint identifies a USB/IP peer by host and port.
	RemoteEndpoint = domain.RemoteEndpoint

	// Event is the closed polymorphic union of domain events emitted
	// on Watch / WatchSessions iterators.
	Event = domain.Event

	// PortID identifies a vhci port numerically.
	PortID = domain.PortID
)
