// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// errUnknownEvent is returned when jsonRenderer.Event receives a
// concrete type not covered by classifyEvent's switch. The sentinel base
// keeps err113 happy while preserving classifiability.
var errUnknownEvent = errors.New("render event: unknown concrete type")

// schemaVersion is the top-level envelope value emitted on every
// JSON output and every jsonlines event record (spec §7.5 stability
// rule). Parsers MUST treat any other string as an incompatible major
// version.
const schemaVersion = "v1"

// Renderer abstracts output formatting so each subcommand writes
// through the same seam regardless of `--output=table|json`. Ack
// responses are JSON-only (spec §7.4.1) and therefore do not appear on
// this interface; callers dispatch to jsonRenderer's typed ack methods
// directly from the JSON branch of each mutating subcommand.
type Renderer interface {
	Devices(w io.Writer, devs []usbip.Device) error
	Ports(w io.Writer, ports []usbip.Port) error
	Sessions(w io.Writer, sessions []usbip.Session) error
	Event(w io.Writer, ev usbip.Event) error
}

// pickRenderer selects the renderer for the output mode. Unknown values
// should have been rejected by validateGlobalFlags; this helper is the
// defensive fallback.
func pickRenderer(output string) Renderer {
	if output == outputJSON {
		return &jsonRenderer{}
	}

	return &tableRenderer{}
}

// jsonRenderer implements Renderer over JSON. Every top-level document
// carries the `"schema": "v1"` envelope. Watch mode emits one record
// per line via Event.
type jsonRenderer struct{}

// Devices emits the list of devices wrapped in a schema envelope. The
// typed struct guarantees "schema" is the first JSON key (spec §7.5
// stability rule) — Go's json.Marshal serialises struct fields in
// source order, unlike map[string]any which sorts alphabetically.
func (jsonRenderer) Devices(w io.Writer, devs []usbip.Device) error {
	return writeJSON(w, devicesEnvelope{
		Schema:  schemaVersion,
		Devices: jsonDevices(devs),
	})
}

// Ports emits the list of ports with a schema-first envelope.
func (jsonRenderer) Ports(w io.Writer, ports []usbip.Port) error {
	return writeJSON(w, portsEnvelope{
		Schema: schemaVersion,
		Ports:  jsonPorts(ports),
	})
}

// Sessions emits the list of sessions with a schema-first envelope.
func (jsonRenderer) Sessions(w io.Writer, sessions []usbip.Session) error {
	return writeJSON(w, sessionsEnvelope{
		Schema:   schemaVersion,
		Sessions: jsonSessions(sessions),
	})
}

// Event emits one jsonlines record tagged with its kind discriminator.
func (jsonRenderer) Event(w io.Writer, ev usbip.Event) error {
	rec := classifyEvent(ev)
	if rec == nil {
		// Unknown concrete type — never happens for domain events the
		// library ships, but we refuse to emit a "v1" record without
		// a stable kind.
		return fmt.Errorf("%w %T", errUnknownEvent, ev)
	}

	return writeJSON(w, rec)
}

// AttachAck writes the attach operation acknowledgement — spec §7.4.1.
// The typed envelope guarantees byte-order (schema → op → ok → port).
func (jsonRenderer) AttachAck(w io.Writer, port usbip.Port) error {
	return writeJSON(w, attachAck{
		ackEnvelope: newAckEnvelope("attach"),
		Port:        newPortView(port),
	})
}

// DetachAck writes the detach operation acknowledgement.
func (jsonRenderer) DetachAck(w io.Writer, portID usbip.PortID) error {
	return writeJSON(w, detachAck{
		ackEnvelope: newAckEnvelope("detach"),
		PortID:      uint64(portID),
	})
}

// BindAck writes the bind operation acknowledgement.
func (jsonRenderer) BindAck(w io.Writer, busID usbip.BusID) error {
	return writeJSON(w, bindAck{
		ackEnvelope: newAckEnvelope("bind"),
		BusID:       string(busID),
	})
}

// UnbindAck writes the unbind operation acknowledgement.
func (jsonRenderer) UnbindAck(w io.Writer, busID usbip.BusID) error {
	return writeJSON(w, unbindAck{
		ackEnvelope: newAckEnvelope("unbind"),
		BusID:       string(busID),
	})
}

// writeJSON marshals v (pretty-compact, single-line) and writes it to w
// with a trailing newline. Keeping the output deterministic makes
// golden-file comparisons easier and matches jsonlines semantics.
func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	// SetEscapeHTML(false) prevents < > & from being escaped in device
	// strings. We never nest our output inside HTML so the escaping
	// just creates spurious bytes.
	enc.SetEscapeHTML(false)

	err := enc.Encode(v)
	if err != nil {
		return fmt.Errorf("encode json: %w", err)
	}

	return nil
}

// devicesEnvelope wraps a device list in the v1 schema-first envelope.
// Schema is declared first so json.Marshal emits it as the leading key.
type devicesEnvelope struct {
	Schema  string       `json:"schema"`
	Devices []deviceView `json:"devices"`
}

// portsEnvelope wraps a port list in the v1 schema-first envelope.
type portsEnvelope struct {
	Schema string     `json:"schema"`
	Ports  []portView `json:"ports"`
}

// sessionsEnvelope wraps a session list in the v1 schema-first envelope.
type sessionsEnvelope struct {
	Schema   string        `json:"schema"`
	Sessions []sessionView `json:"sessions"`
}

// jsonDevices converts a []usbip.Device into a slice of v1 deviceView
// records. Using the shared view type keeps list and event shapes in
// lockstep for downstream consumers.
func jsonDevices(devs []usbip.Device) []deviceView {
	out := make([]deviceView, 0, len(devs))
	for _, d := range devs {
		out = append(out, newDeviceView(d))
	}

	return out
}

// jsonPorts converts ports to v1 portView records.
func jsonPorts(ports []usbip.Port) []portView {
	out := make([]portView, 0, len(ports))
	for _, p := range ports {
		out = append(out, newPortView(p))
	}

	return out
}

// jsonSessions converts sessions to v1 sessionView records.
func jsonSessions(sessions []usbip.Session) []sessionView {
	out := make([]sessionView, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, newSessionView(s))
	}

	return out
}

// formatHex16 renders a 16-bit USB id as a 4-digit lowercase hex string,
// consistent with the `lsusb` convention.
func formatHex16(v uint16) string {
	return fmt.Sprintf("%04x", v)
}
