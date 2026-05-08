package main

import (
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// eventRecorder converts a specific domain event concrete type to its
// v1 jsonlines representation. Registered per kind via the map-based
// dispatch in eventRecord.
type eventRecorder func(usbip.Event) map[string]any

// eventRecorders is the closed dispatch table from EventKind to the
// concrete-type-aware recorder. Using a map instead of a switch keeps
// eventRecord under the cyclop cap of 10.
func eventRecorders() map[domain.EventKind]eventRecorder {
	return map[domain.EventKind]eventRecorder{
		domain.EventPortAttached:        adaptPortAttached,
		domain.EventPortDetached:        adaptPortDetached,
		domain.EventPortErrored:         adaptPortErrored,
		domain.EventDeviceBound:         adaptDeviceBound,
		domain.EventDeviceUnbound:       adaptDeviceUnbound,
		domain.EventRemoteDeviceAdded:   adaptRemoteDeviceAdded,
		domain.EventRemoteDeviceRemoved: adaptRemoteDeviceRemoved,
		domain.EventSessionStarted:      adaptSessionStarted,
		domain.EventSessionEnded:        adaptSessionEnded,
	}
}

// eventRecord converts a domain event into the v1 jsonlines map used by
// jsonRenderer.Event. nil is returned for unknown concrete types so the
// caller can surface the classification failure.
func eventRecord(ev usbip.Event) map[string]any {
	rec, ok := eventRecorders()[ev.EventKind()]
	if !ok {
		return nil
	}

	return rec(ev)
}

func adaptPortAttached(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.PortAttachedEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema": schemaVersion,
		"kind":   e.EventKind().String(),
		"at":     formatTime(e.At),
		"port":   portMap(e.Port),
	}
}

func adaptPortDetached(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.PortDetachedEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema": schemaVersion,
		"kind":   e.EventKind().String(),
		"at":     formatTime(e.At),
		"port":   portMap(e.Port),
		"reason": e.Reason,
	}
}

func adaptPortErrored(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.PortErroredEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema": schemaVersion,
		"kind":   e.EventKind().String(),
		"at":     formatTime(e.At),
		"port":   portMap(e.Port),
		"err":    e.Err,
	}
}

func adaptDeviceBound(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.DeviceBoundEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema": schemaVersion,
		"kind":   e.EventKind().String(),
		"at":     formatTime(e.At),
		"device": deviceMap(e.Device),
	}
}

func adaptDeviceUnbound(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.DeviceUnboundEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema": schemaVersion,
		"kind":   e.EventKind().String(),
		"at":     formatTime(e.At),
		"device": deviceMap(e.Device),
	}
}

func adaptRemoteDeviceAdded(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.RemoteDeviceAddedEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema": schemaVersion,
		"kind":   e.EventKind().String(),
		"at":     formatTime(e.At),
		"remote": e.Remote.String(),
		"device": deviceMap(e.Device),
	}
}

func adaptRemoteDeviceRemoved(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.RemoteDeviceRemovedEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema": schemaVersion,
		"kind":   e.EventKind().String(),
		"at":     formatTime(e.At),
		"remote": e.Remote.String(),
		"busid":  string(e.BusID),
	}
}

func adaptSessionStarted(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.SessionStartedEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema":  schemaVersion,
		"kind":    e.EventKind().String(),
		"at":      formatTime(e.At),
		"session": sessionMap(e.Session),
	}
}

func adaptSessionEnded(ev usbip.Event) map[string]any {
	e, ok := ev.(domain.SessionEndedEvent)
	if !ok {
		return nil
	}

	return map[string]any{
		"schema":  schemaVersion,
		"kind":    e.EventKind().String(),
		"at":      formatTime(e.At),
		"session": sessionMap(e.Session),
		"reason":  e.Reason,
	}
}

// formatTime renders t in RFC 3339 nano-second form UTC. The format is
// locked as part of the v1 schema.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// deviceMap is the single-device v1 representation; used as a shared
// field inside multiple event records.
func deviceMap(d usbip.Device) map[string]any {
	return map[string]any{
		"busid":      string(d.BusID),
		"busnum":     d.BusNum,
		"devnum":     d.DevNum,
		"speed":      d.Speed.String(),
		"vendor_id":  formatHex16(d.VendorID),
		"product_id": formatHex16(d.ProductID),
	}
}

// portMap is the single-port v1 representation.
func portMap(p usbip.Port) map[string]any {
	return map[string]any{
		"id":          uint32(p.ID),
		"status":      p.Status.String(),
		"speed":       p.Speed.String(),
		"remote":      p.Remote.String(),
		"busid":       string(p.BusID),
		"local_busid": string(p.LocalBusID),
	}
}

// sessionMap is the single-session v1 representation.
func sessionMap(s usbip.Session) map[string]any {
	return map[string]any{
		"id":         s.ID.String(),
		"remote":     s.RemoteAddr.String(),
		"busid":      string(s.BusID),
		"started_at": formatTime(s.StartedAt),
		"bytes_in":   s.BytesIn,
		"bytes_out":  s.BytesOut,
	}
}
