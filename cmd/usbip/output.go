package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"time"

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
// through the same seam regardless of `--output=table|json`.
type Renderer interface {
	Devices(w io.Writer, devs []usbip.Device) error
	Ports(w io.Writer, ports []usbip.Port) error
	Sessions(w io.Writer, sessions []usbip.Session) error
	Event(w io.Writer, ev usbip.Event) error
	Ack(w io.Writer, op string, extra map[string]any) error
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

// Devices emits the list of devices wrapped in a schema envelope.
func (jsonRenderer) Devices(w io.Writer, devs []usbip.Device) error {
	return writeJSON(w, map[string]any{
		"schema":  schemaVersion,
		"devices": jsonDevices(devs),
	})
}

// Ports emits the list of ports.
func (jsonRenderer) Ports(w io.Writer, ports []usbip.Port) error {
	return writeJSON(w, map[string]any{
		"schema": schemaVersion,
		"ports":  jsonPorts(ports),
	})
}

// Sessions emits the list of sessions.
func (jsonRenderer) Sessions(w io.Writer, sessions []usbip.Session) error {
	return writeJSON(w, map[string]any{
		"schema":   schemaVersion,
		"sessions": jsonSessions(sessions),
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

// Ack emits a one-line JSON operation acknowledgement for mutating
// subcommands (attach/detach/bind/unbind) per spec §7.4.1.
func (jsonRenderer) Ack(w io.Writer, op string, extra map[string]any) error {
	rec := map[string]any{
		"schema": schemaVersion,
		"op":     op,
		"ok":     true,
	}

	maps.Copy(rec, extra)

	return writeJSON(w, rec)
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

// jsonDevices converts a []usbip.Device into a slice of stable map
// records suitable for the v1 schema. The explicit mapping decouples
// JSON shape from internal struct evolution.
func jsonDevices(devs []usbip.Device) []map[string]any {
	out := make([]map[string]any, 0, len(devs))

	for _, d := range devs {
		out = append(out, map[string]any{
			"path":       d.Path,
			"busid":      string(d.BusID),
			"busnum":     d.BusNum,
			"devnum":     d.DevNum,
			"speed":      d.Speed.String(),
			"vendor_id":  formatHex16(d.VendorID),
			"product_id": formatHex16(d.ProductID),
			"bcd_device": formatHex16(d.BcdDevice),
			"class":      uint8(d.Class),
			"subclass":   uint8(d.Subclass),
			"protocol":   uint8(d.Protocol),
		})
	}

	return out
}

// jsonPorts converts ports to v1 map records.
func jsonPorts(ports []usbip.Port) []map[string]any {
	out := make([]map[string]any, 0, len(ports))

	for _, p := range ports {
		out = append(out, map[string]any{
			"id":           uint32(p.ID),
			"status":       p.Status.String(),
			"speed":        p.Speed.String(),
			"device_id":    uint32(p.DeviceID),
			"remote":       p.Remote.String(),
			"busid":        string(p.BusID),
			"local_busid":  string(p.LocalBusID),
		})
	}

	return out
}

// jsonSessions converts sessions to v1 map records.
func jsonSessions(sessions []usbip.Session) []map[string]any {
	out := make([]map[string]any, 0, len(sessions))

	for _, s := range sessions {
		out = append(out, map[string]any{
			"id":          s.ID.String(),
			"remote":      s.RemoteAddr.String(),
			"busid":       string(s.BusID),
			"started_at":  s.StartedAt.UTC().Format(time.RFC3339Nano),
			"bytes_in":    s.BytesIn,
			"bytes_out":   s.BytesOut,
		})
	}

	return out
}

// formatHex16 renders a 16-bit USB id as a 4-digit lowercase hex string,
// consistent with the `lsusb` convention.
func formatHex16(v uint16) string {
	return fmt.Sprintf("%04x", v)
}
