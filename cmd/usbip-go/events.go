// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// Event-record structs are the v1 jsonlines shape. json tags lock the
// wire field names; omitting a field from the struct removes it from
// the emitted record, so every current v1 field is present explicitly.

type eventBase struct {
	Schema string `json:"schema"`
	Kind   string `json:"kind"`
	At     string `json:"at"`
}

type deviceView struct {
	BusID     string `json:"busid"`
	BusNum    uint16 `json:"busnum"`
	DevNum    uint16 `json:"devnum"`
	Speed     string `json:"speed"`
	VendorID  string `json:"vendor_id"`
	ProductID string `json:"product_id"`
}

type portView struct {
	ID         uint32 `json:"id"`
	Status     string `json:"status"`
	Speed      string `json:"speed"`
	Remote     string `json:"remote"`
	BusID      string `json:"busid"`
	LocalBusID string `json:"local_busid"`
}

type sessionView struct {
	ID        string `json:"id"`
	Remote    string `json:"remote"`
	BusID     string `json:"busid"`
	StartedAt string `json:"started_at"`
	BytesIn   uint64 `json:"bytes_in"`
	BytesOut  uint64 `json:"bytes_out"`
}

// ackEnvelope is the common prefix of every JSON ack record: schema,
// op, and ok. Embedded as the first field of the per-op ack structs so
// json.Marshal emits these keys before any op-specific payload (spec
// §7.5 stability rule: "schema" is always the first byte-order key).
type ackEnvelope struct {
	Schema string `json:"schema"`
	Op     string `json:"op"`
	OK     bool   `json:"ok"`
}

// attachAck is the `attach --output=json` response envelope.
type attachAck struct {
	ackEnvelope

	Port portView `json:"port"`
}

// detachAck is the `detach --output=json` response envelope.
type detachAck struct {
	ackEnvelope

	PortID uint64 `json:"port_id"`
}

// bindAck is the `bind --output=json` response envelope.
type bindAck struct {
	ackEnvelope

	BusID string `json:"busid"`
}

// unbindAck is the `unbind --output=json` response envelope. A
// dedicated type (vs. reusing bindAck) keeps op→struct mapping
// monomorphic and makes future per-op evolution local.
type unbindAck struct {
	ackEnvelope

	BusID string `json:"busid"`
}

// newAckEnvelope builds a v1 ackEnvelope for the given op name with
// OK=true. All ack records in the CLI today report success (failures
// surface as non-zero exit codes, never as {"ok":false}).
func newAckEnvelope(op string) ackEnvelope {
	return ackEnvelope{
		Schema: schemaVersion,
		Op:     op,
		OK:     true,
	}
}

type portAttachedRecord struct {
	eventBase

	Port portView `json:"port"`
}

type portDetachedRecord struct {
	eventBase

	Port   portView `json:"port"`
	Reason string   `json:"reason"`
}

type portErroredRecord struct {
	eventBase

	Port portView `json:"port"`
	Err  string   `json:"err"`
}

type deviceBoundRecord struct {
	eventBase

	Device deviceView `json:"device"`
}

type deviceUnboundRecord struct {
	eventBase

	Device deviceView `json:"device"`
}

type remoteDeviceAddedRecord struct {
	eventBase

	Remote string     `json:"remote"`
	Device deviceView `json:"device"`
}

type remoteDeviceRemovedRecord struct {
	eventBase

	Remote string `json:"remote"`
	BusID  string `json:"busid"`
}

type sessionStartedRecord struct {
	eventBase

	Session sessionView `json:"session"`
}

type sessionEndedRecord struct {
	eventBase

	Session sessionView `json:"session"`
	Reason  string      `json:"reason"`
}

// eventRecorder converts a domain event into its v1-schema record struct.
// Returns nil when ev's concrete type does not match the expected kind.
type eventRecorder func(usbip.Event) any

// eventRecorders is the closed dispatch table from EventKind to the
// concrete-type-aware recorder. Map-over-switch keeps classifyEvent
// under the cyclop cap of 10.
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

// classifyEvent converts a domain event into its v1-schema record
// struct. nil is returned for unknown concrete types so the caller can
// surface the classification failure.
func classifyEvent(ev usbip.Event) any {
	rec, ok := eventRecorders()[ev.EventKind()]
	if !ok {
		return nil
	}

	return rec(ev)
}

// eventHeader extracts the Kind and At from a record returned by
// classifyEvent. All event records embed eventBase, so a type
// assertion to that embedded struct would be noise; returning via a
// small helper keeps callers out of reflection and stringly-typed
// lookups. The boolean is false only when rec is of an unknown type
// (classifyEvent returned nil).
func eventHeader(rec any) (string, string, bool) {
	switch r := rec.(type) {
	case portAttachedRecord:
		return r.Kind, r.At, true
	case portDetachedRecord:
		return r.Kind, r.At, true
	case portErroredRecord:
		return r.Kind, r.At, true
	case deviceBoundRecord:
		return r.Kind, r.At, true
	case deviceUnboundRecord:
		return r.Kind, r.At, true
	case remoteDeviceAddedRecord:
		return r.Kind, r.At, true
	case remoteDeviceRemovedRecord:
		return r.Kind, r.At, true
	case sessionStartedRecord:
		return r.Kind, r.At, true
	case sessionEndedRecord:
		return r.Kind, r.At, true
	}

	return "", "", false
}

func newEventBase(k domain.EventKind, at time.Time) eventBase {
	return eventBase{
		Schema: schemaVersion,
		Kind:   k.String(),
		At:     formatTime(at),
	}
}

func adaptPortAttached(ev usbip.Event) any {
	e, ok := ev.(domain.PortAttachedEvent)
	if !ok {
		return nil
	}

	return portAttachedRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Port:      newPortView(e.Port),
	}
}

func adaptPortDetached(ev usbip.Event) any {
	e, ok := ev.(domain.PortDetachedEvent)
	if !ok {
		return nil
	}

	return portDetachedRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Port:      newPortView(e.Port),
		Reason:    e.Reason,
	}
}

func adaptPortErrored(ev usbip.Event) any {
	e, ok := ev.(domain.PortErroredEvent)
	if !ok {
		return nil
	}

	return portErroredRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Port:      newPortView(e.Port),
		Err:       e.Err,
	}
}

func adaptDeviceBound(ev usbip.Event) any {
	e, ok := ev.(domain.DeviceBoundEvent)
	if !ok {
		return nil
	}

	return deviceBoundRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Device:    newDeviceView(e.Device),
	}
}

func adaptDeviceUnbound(ev usbip.Event) any {
	e, ok := ev.(domain.DeviceUnboundEvent)
	if !ok {
		return nil
	}

	return deviceUnboundRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Device:    newDeviceView(e.Device),
	}
}

func adaptRemoteDeviceAdded(ev usbip.Event) any {
	e, ok := ev.(domain.RemoteDeviceAddedEvent)
	if !ok {
		return nil
	}

	return remoteDeviceAddedRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Remote:    e.Remote.String(),
		Device:    newDeviceView(e.Device),
	}
}

func adaptRemoteDeviceRemoved(ev usbip.Event) any {
	e, ok := ev.(domain.RemoteDeviceRemovedEvent)
	if !ok {
		return nil
	}

	return remoteDeviceRemovedRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Remote:    e.Remote.String(),
		BusID:     string(e.BusID),
	}
}

func adaptSessionStarted(ev usbip.Event) any {
	e, ok := ev.(domain.SessionStartedEvent)
	if !ok {
		return nil
	}

	return sessionStartedRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Session:   newSessionView(e.Session),
	}
}

func adaptSessionEnded(ev usbip.Event) any {
	e, ok := ev.(domain.SessionEndedEvent)
	if !ok {
		return nil
	}

	return sessionEndedRecord{
		eventBase: newEventBase(e.EventKind(), e.At),
		Session:   newSessionView(e.Session),
		Reason:    e.Reason,
	}
}

// formatTime renders t in RFC 3339 nano-second form UTC. The format is
// locked as part of the v1 schema.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func newDeviceView(d usbip.Device) deviceView {
	return deviceView{
		BusID:     string(d.BusID),
		BusNum:    d.BusNum,
		DevNum:    d.DevNum,
		Speed:     d.Speed.String(),
		VendorID:  formatHex16(d.VendorID),
		ProductID: formatHex16(d.ProductID),
	}
}

func newPortView(p usbip.Port) portView {
	return portView{
		ID:         uint32(p.ID),
		Status:     p.Status.String(),
		Speed:      p.Speed.String(),
		Remote:     p.Remote.String(),
		BusID:      string(p.BusID),
		LocalBusID: string(p.LocalBusID),
	}
}

func newSessionView(s usbip.Session) sessionView {
	return sessionView{
		ID:        s.ID.String(),
		Remote:    s.RemoteAddr.String(),
		BusID:     string(s.BusID),
		StartedAt: formatTime(s.StartedAt),
		BytesIn:   s.BytesIn,
		BytesOut:  s.BytesOut,
	}
}
