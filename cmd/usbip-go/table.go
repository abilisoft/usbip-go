// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/olekukonko/tablewriter"
)

// tableRenderer implements Renderer over a human-readable column
// layout. Layout is NOT a stable contract (v1 contract §7.5 note); the JSON
// renderer is the stable counterpart.
type tableRenderer struct{}

// Devices writes a table of local/remote devices.
func (tableRenderer) Devices(w io.Writer, devs []usbip.Device) error {
	t := tablewriter.NewWriter(w)
	t.SetHeader([]string{"BUSID", "SPEED", "VID:PID", "CLASS"})
	t.SetAutoWrapText(false)
	t.SetBorder(false)

	for _, d := range devs {
		t.Append([]string{
			string(d.BusID),
			d.Speed.String(),
			fmt.Sprintf("%04x:%04x", d.VendorID, d.ProductID),
			strconv.Itoa(int(d.Class)),
		})
	}

	t.Render()

	return nil
}

// Ports writes a table of attached vhci ports.
func (tableRenderer) Ports(w io.Writer, ports []usbip.Port) error {
	t := tablewriter.NewWriter(w)
	t.SetHeader([]string{"PORT", "STATUS", "SPEED", "REMOTE", "BUSID", "LOCAL"})
	t.SetAutoWrapText(false)
	t.SetBorder(false)

	for _, p := range ports {
		t.Append([]string{
			strconv.FormatUint(uint64(p.ID), 10),
			p.Status.String(),
			p.Speed.String(),
			p.Remote.String(),
			string(p.BusID),
			string(p.LocalBusID),
		})
	}

	t.Render()

	return nil
}

// Sessions writes a table of daemon sessions.
func (tableRenderer) Sessions(w io.Writer, sessions []usbip.Session) error {
	t := tablewriter.NewWriter(w)
	t.SetHeader([]string{"ID", "REMOTE", "BUSID", "STARTED", "IN", "OUT"})
	t.SetAutoWrapText(false)
	t.SetBorder(false)

	for _, s := range sessions {
		t.Append([]string{
			s.ID.String(),
			s.RemoteAddr.String(),
			string(s.BusID),
			s.StartedAt.Format("2006-01-02T15:04:05Z"),
			strconv.FormatUint(s.BytesIn, 10),
			strconv.FormatUint(s.BytesOut, 10),
		})
	}

	t.Render()

	return nil
}

// Event writes a one-line textual representation of ev to w. The table
// renderer format is intentionally compact and NOT machine-readable;
// --output=json is the stable watch stream.
func (tableRenderer) Event(w io.Writer, ev usbip.Event) error {
	rec := classifyEvent(ev)
	kind, at, ok := eventHeader(rec)

	if !ok {
		_, err := fmt.Fprintf(w, "%T %v\n", ev, ev)
		if err != nil {
			return fmt.Errorf("render event: %w", err)
		}

		return nil
	}

	_, err := fmt.Fprintf(w, "%s %s\n", kind, at)
	if err != nil {
		return fmt.Errorf("render event: %w", err)
	}

	return nil
}
