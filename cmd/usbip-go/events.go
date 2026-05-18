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

type attachAck struct {
	ackEnvelope

	Port portView `json:"port"`
}

type detachAck struct {
	ackEnvelope

	PortID uint64 `json:"port_id"`
}

type bindAck struct {
	ackEnvelope

	BusID string `json:"busid"`
}

type unbindAck struct {
	ackEnvelope

	BusID string `json:"busid"`
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

type portReconnectExhaustedRecord struct {
	eventBase

	Port      portView `json:"port"`
	Attempts  int      `json:"attempts"`
	LastError string   `json:"last_error"`
}

type deviceBoundRecord struct {
	eventBase

	Device deviceView `json:"device"`
}

type deviceUnboundRecord struct {
	eventBase

	Device deviceView `json:"device"`
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

func newAckEnvelope(op string) ackEnvelope {
	return ackEnvelope{
		Schema: schemaVersion,
		Op:     op,
		OK:     true,
	}
}

func classifyEvent(ev usbip.Event) any {
	switch e := ev.(type) {
	case domain.PortAttachedEvent:
		return portAttachedRecord{
			eventBase: newEventBase(e.EventKind(), e.At),
			Port:      newPortView(e.Port),
		}
	case domain.PortDetachedEvent:
		return portDetachedRecord{
			eventBase: newEventBase(e.EventKind(), e.At),
			Port:      newPortView(e.Port),
			Reason:    e.Reason,
		}
	case domain.PortErroredEvent:
		return portErroredRecord{
			eventBase: newEventBase(e.EventKind(), e.At),
			Port:      newPortView(e.Port),
			Err:       e.Err,
		}
	case domain.PortReconnectExhaustedEvent:
		return portReconnectExhaustedRecord{
			eventBase: newEventBase(e.EventKind(), e.At),
			Port:      newPortView(e.Port),
			Attempts:  e.Attempts,
			LastError: e.LastError,
		}
	case domain.DeviceBoundEvent:
		return deviceBoundRecord{
			eventBase: newEventBase(e.EventKind(), e.At),
			Device:    newDeviceView(e.Device),
		}
	case domain.DeviceUnboundEvent:
		return deviceUnboundRecord{
			eventBase: newEventBase(e.EventKind(), e.At),
			Device:    newDeviceView(e.Device),
		}
	case domain.SessionStartedEvent:
		return sessionStartedRecord{
			eventBase: newEventBase(e.EventKind(), e.At),
			Session:   newSessionView(e.Session),
		}
	case domain.SessionEndedEvent:
		return sessionEndedRecord{
			eventBase: newEventBase(e.EventKind(), e.At),
			Session:   newSessionView(e.Session),
			Reason:    e.Reason,
		}
	default:
		return nil
	}
}

func eventHeader(rec any) (string, string, bool) {
	switch r := rec.(type) {
	case portAttachedRecord:
		return r.Kind, r.At, true
	case portDetachedRecord:
		return r.Kind, r.At, true
	case portErroredRecord:
		return r.Kind, r.At, true
	case portReconnectExhaustedRecord:
		return r.Kind, r.At, true
	case deviceBoundRecord:
		return r.Kind, r.At, true
	case deviceUnboundRecord:
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
