// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// Style palette. Color profile detection happens at the writer
// boundary (lipgloss honors NO_COLOR and TTY auto-detection); the
// `--no-color` flag forces plain output by replacing the renderer
// with a degraded profile in pickRenderer.
//
// Codepoints stay ASCII so terminals without unicode font fallback
// still render badges; styles only add color/bold, never replace
// content with glyphs the operator might not have in their
// font.

// styledHeader bolds the column header label and tints it cyan so
// the eye lands on the row separator without having to follow indent.
var styledHeader = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#5fd7ff")).
	Padding(0, 1)

// styledCell pads each row cell uniformly so the table columns
// align even when adjacent cells differ in foreground intensity.
var styledCell = lipgloss.NewStyle().Padding(0, 1)

// statusStyle returns the foreground style for a port-status
// label. Maps the operator-facing semantic of the status to a
// reasonable color: green for "currently attached", yellow for
// "negotiating", red for "error/lost", cyan for "free/idle".
func statusStyle(status string) lipgloss.Style {
	base := styledCell

	switch strings.ToLower(status) {
	case "used", "active", "in_use", "attached":
		return base.Foreground(lipgloss.Color("#5fff87")).Bold(true)
	case "error", "lost", "disconnected", "failed":
		return base.Foreground(lipgloss.Color("#ff5f5f")).Bold(true)
	case "available", "idle", "free", "ready":
		return base.Foreground(lipgloss.Color("#5fd7ff"))
	case "pending", "connecting", "negotiating":
		return base.Foreground(lipgloss.Color("#ffd75f"))
	default:
		return base
	}
}

// speedStyle returns the foreground style for a USB speed label so
// the table communicates the device's tier at a glance: dim for low,
// white for full, blue for high, magenta for SuperSpeed and brighter
// magenta+bold for SuperSpeed+.
func speedStyle(speed usbip.Speed) lipgloss.Style {
	base := styledCell

	switch speed {
	case usbip.Speed(domain.SpeedUnknown):
		return base.Foreground(lipgloss.Color("#878787")) // dim gray
	case usbip.Speed(domain.SpeedLow):
		return base.Foreground(lipgloss.Color("#a8a8a8")) // gray
	case usbip.Speed(domain.SpeedFull):
		return base.Foreground(lipgloss.Color("#ffffff")) // white
	case usbip.Speed(domain.SpeedHigh):
		return base.Foreground(lipgloss.Color("#5f87ff")) // blue
	case usbip.Speed(domain.SpeedWireless):
		return base.Foreground(lipgloss.Color("#af5fff")) // purple
	case usbip.Speed(domain.SpeedSuper):
		return base.Foreground(lipgloss.Color("#ff5fff")) // magenta
	case usbip.Speed(domain.SpeedSuperPlus):
		return base.Foreground(lipgloss.Color("#ff5fff")).Bold(true) // bold magenta
	default:
		return base
	}
}

// dimStyle is for less-important columns (BUSID separators, hyphens,
// trailing metadata).
var dimStyle = styledCell.Foreground(lipgloss.Color("#878787"))

// emphasizeStyle is for primary identifiers (busid, port id) that
// the operator scans for.
var emphasizeStyle = styledCell.Bold(true)
