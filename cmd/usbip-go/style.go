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
// boundary (colorprofile honors NO_COLOR and TTY auto-detection);
// the `--no-color` flag sets NO_COLOR=1 early so colorprofile
// degrades to plain output through the same path.
//
// Cell content stays ASCII so terminals without a unicode font
// fallback still render values verbatim. Border glyphs are
// lipgloss.RoundedBorder()'s box-drawing characters (U+256D etc.),
// emitted only by the chrome — not by any cell. colorprofile
// strips ANSI escapes for non-TTY destinations but does NOT
// transliterate unicode glyphs; a terminal lacking those code
// points renders the chrome as missing-glyph squares. Operators
// who need ASCII-only borders should pipe through `--output=json`
// (the stable scriptable contract) instead.

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
	case domain.SpeedUnknown:
		return base.Foreground(lipgloss.Color("#878787")) // dim gray
	case domain.SpeedLow:
		return base.Foreground(lipgloss.Color("#a8a8a8")) // gray
	case domain.SpeedFull:
		return base.Foreground(lipgloss.Color("#ffffff")) // white
	case domain.SpeedHigh:
		return base.Foreground(lipgloss.Color("#5f87ff")) // blue
	case domain.SpeedWireless:
		return base.Foreground(lipgloss.Color("#af5fff")) // purple
	case domain.SpeedSuper:
		return base.Foreground(lipgloss.Color("#ff5fff")) // magenta
	case domain.SpeedSuperPlus:
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

// successMarkStyle renders the "✓" prefix used by bind/unbind/attach/
// detach acks. Bright green so the eye lands on the success token
// without parsing the action verb. Padding 0 because the ack
// composes "<mark> <verb> <subject>" inline.
var successMarkStyle = lipgloss.NewStyle().
	Bold(true).
	Foreground(lipgloss.Color("#5fff87"))

// actionStyle bolds the verb in an ack ("bound", "attached", etc.)
// without coloring it; the color comes from successMarkStyle so the
// verb itself stays readable on every theme.
var actionStyle = lipgloss.NewStyle().Bold(true)

// subjectStyle highlights the busid/port-id the ack ran against.
// Same cyan as styledHeader so the operator's eye recognizes the
// "this is the identifier" intent across help, tables, and acks.
var subjectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fd7ff"))

// formatAck composes "✓ <action> <subject>" with the palette above.
// The mark glyph is non-ASCII; styleWriter strips ANSI for plain
// terminals via colorprofile but does not transliterate the glyph
// (operators on plain terminals see "?"). Acceptable trade-off
// versus the historical plain-text ack for the value the green
// mark adds in the common interactive case.
func formatAck(action, subject string) string {
	const successMark = "✓"

	return successMarkStyle.Render(successMark) + " " +
		actionStyle.Render(action) + " " +
		subjectStyle.Render(subject)
}
