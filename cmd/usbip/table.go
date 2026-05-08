// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/colorprofile"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// styleWriter wraps w so lipgloss-emitted ANSI escape sequences are
// downgraded according to the actual terminal capability:
//
//   - TTY with truecolor support → keep ANSI verbatim
//   - dumb / piped to file or buffer → strip ANSI entirely
//   - NO_COLOR=<any non-empty value> → strip ANSI entirely
//     (no-color.org spec: any non-empty value disables color)
//
// Centralised so every render method emits through one degradation
// boundary instead of each writer guessing at the profile.
//
// Implementation note: colorprofile.NewWriter consults
// strconv.ParseBool on NO_COLOR, which only honors boolean-like
// values ("1", "true", "yes" returns false from ParseBool, etc.).
// That contradicts the no-color.org spec the operator-facing
// documentation promises. We pre-normalize: if NO_COLOR is set to
// any non-empty value, force the env handed to colorprofile to
// "NO_COLOR=1" so its ParseBool path enables the no-color branch
// regardless of the original value.
func styleWriter(w io.Writer) io.Writer {
	env := os.Environ()
	if os.Getenv("NO_COLOR") != "" {
		env = append(env, "NO_COLOR=1")
	}

	return colorprofile.NewWriter(w, env)
}

// tableRenderer implements Renderer over a styled human-readable
// table layout. Layout is NOT a stable contract (v1 contract §7.5
// note); the JSON renderer is the stable counterpart for scripts.
//
// Styling is delegated to lipgloss/v2; color profile detection is
// automatic — when stdout is not a TTY (pipe, file redirect) lipgloss
// degrades to plain text. The --no-color flag wires through pickRenderer.
type tableRenderer struct{}

// borderStyle defines the table chrome: rounded box-drawing corners
// (lipgloss.RoundedBorder, U+256D and friends) with dim foreground
// so the eye lands on the row contents, not the frame. Plain
// terminals are handled by colorprofile downstream.
var borderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5f5f5f"))

// newStyledTable constructs a lipgloss table preconfigured with
// rounded borders, a styled header row, and the per-column cell
// styler the caller supplies. Centralised so every render method
// shares one chrome and column-style policy.
func newStyledTable(cellStyler func(row, col int, value string) lipgloss.Style) *table.Table {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(borderStyle).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return styledHeader
			}

			return cellStyler(row, col, "")
		})
}

// Devices writes a styled table of local/remote devices.
//
// Columns:
//   - BUSID:  sysfs bus identifier
//   - DEVICE: device-supplied iManufacturer + iProduct strings, dash if absent
//   - SPEED:  enumerated USB link speed
//   - VID:PID: idVendor:idProduct hex pair
//   - CLASS:  USB device class as human name (Class.String())
//   - IF:     bNumInterfaces — useful sanity-check for composite devices
func (tableRenderer) Devices(w io.Writer, devs []usbip.Device) error {
	rows := make([][]string, 0, len(devs))
	speeds := make([]usbip.Speed, 0, len(devs))

	for _, d := range devs {
		rows = append(rows, []string{
			string(d.BusID),
			deviceLabel(d),
			d.Speed.String(),
			fmt.Sprintf("%04x:%04x", d.VendorID, d.ProductID),
			d.Class.String(),
			strconv.Itoa(int(d.NumInterfaces)),
		})
		speeds = append(speeds, d.Speed)
	}

	const (
		colBusID  = 0
		colDevice = 1
		colSpeed  = 2
	)

	t := newStyledTable(func(row, col int, _ string) lipgloss.Style {
		switch col {
		case colBusID:
			return emphasizeStyle
		case colDevice:
			return emphasizeStyle
		case colSpeed:
			return speedStyle(speeds[row])
		default:
			return styledCell
		}
	}).
		Headers("BUSID", "DEVICE", "SPEED", "VID:PID", "CLASS", "IF").
		Rows(rows...)

	_, err := fmt.Fprintln(styleWriter(w), t.Render())
	if err != nil {
		return fmt.Errorf("render device table: %w", err)
	}

	return nil
}

// deviceLabel formats a human-friendly device name using the
// iManufacturer + iProduct strings the device reports via sysfs.
// Falls back to "—" when both are empty (string descriptors not
// populated, common on cheap composite devices).
func deviceLabel(d usbip.Device) string {
	if d.Manufacturer == "" && d.Product == "" {
		return "—"
	}

	if d.Manufacturer == "" {
		return d.Product
	}

	if d.Product == "" {
		return d.Manufacturer
	}

	return d.Manufacturer + " " + d.Product
}

// Ports writes a styled table of attached vhci ports.
func (tableRenderer) Ports(w io.Writer, ports []usbip.Port) error {
	rows := make([][]string, 0, len(ports))
	speeds := make([]usbip.Speed, 0, len(ports))
	statuses := make([]string, 0, len(ports))

	for _, p := range ports {
		statusStr := p.Status.String()

		rows = append(rows, []string{
			strconv.FormatUint(uint64(p.ID), 10),
			statusStr,
			p.Speed.String(),
			p.Remote.String(),
			string(p.BusID),
			string(p.LocalBusID),
		})
		speeds = append(speeds, p.Speed)
		statuses = append(statuses, statusStr)
	}

	const (
		colPort   = 0
		colStatus = 1
		colSpeed  = 2
	)

	t := newStyledTable(func(row, col int, _ string) lipgloss.Style {
		switch col {
		case colPort:
			return emphasizeStyle
		case colStatus:
			return statusStyle(statuses[row])
		case colSpeed:
			return speedStyle(speeds[row])
		default:
			return styledCell
		}
	}).
		Headers("PORT", "STATUS", "SPEED", "REMOTE", "BUSID", "LOCAL").
		Rows(rows...)

	_, err := fmt.Fprintln(styleWriter(w), t.Render())
	if err != nil {
		return fmt.Errorf("render port table: %w", err)
	}

	return nil
}

// Sessions writes a styled table of daemon sessions.
func (tableRenderer) Sessions(w io.Writer, sessions []usbip.Session) error {
	rows := make([][]string, 0, len(sessions))

	for _, s := range sessions {
		rows = append(rows, []string{
			s.ID.String(),
			s.RemoteAddr.String(),
			string(s.BusID),
			s.StartedAt.Format("2006-01-02T15:04:05Z"),
			strconv.FormatUint(s.BytesIn, 10),
			strconv.FormatUint(s.BytesOut, 10),
		})
	}

	const colSessionID = 0

	t := newStyledTable(func(_, col int, _ string) lipgloss.Style {
		if col == colSessionID {
			return emphasizeStyle
		}

		return styledCell
	}).
		Headers("ID", "REMOTE", "BUSID", "STARTED", "IN", "OUT").
		Rows(rows...)

	_, err := fmt.Fprintln(styleWriter(w), t.Render())
	if err != nil {
		return fmt.Errorf("render session table: %w", err)
	}

	return nil
}

// Event writes a one-line textual representation of ev to w. The
// table renderer format is intentionally compact and NOT
// machine-readable; --output=json is the stable watch stream.
//
// Style output goes through styleWriter so --no-color, NO_COLOR, and
// non-TTY destinations strip ANSI uniformly with the table renderers.
func (tableRenderer) Event(w io.Writer, ev usbip.Event) error {
	out := styleWriter(w)
	rec := classifyEvent(ev)
	kind, at, ok := eventHeader(rec)

	if !ok {
		_, err := fmt.Fprintf(out, "%T %v\n", ev, ev)
		if err != nil {
			return fmt.Errorf("render event: %w", err)
		}

		return nil
	}

	_, err := fmt.Fprintf(out, "%s %s\n", emphasizeStyle.Render(kind), dimStyle.Render(at))
	if err != nil {
		return fmt.Errorf("render event: %w", err)
	}

	return nil
}
