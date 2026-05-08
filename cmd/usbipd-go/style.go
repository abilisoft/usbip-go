// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// styleWriter wraps w through colorprofile so lipgloss-emitted ANSI
// downgrades to plain text on non-TTY destinations and when NO_COLOR
// is set to any non-empty value (no-color.org spec). Mirrors the
// helper in cmd/usbip-go/table.go so both binaries degrade through
// one boundary.
//
// Pre-normalises NO_COLOR=<any-non-empty> to NO_COLOR=1 because
// colorprofile's underlying ParseBool only accepts boolean-like
// values; without this "yes"/"on"/arbitrary strings would leave
// color enabled on TTYs despite the spec.
func styleWriter(w io.Writer) io.Writer {
	env := os.Environ()
	if os.Getenv("NO_COLOR") != "" {
		env = append(env, "NO_COLOR=1")
	}

	return colorprofile.NewWriter(w, env)
}

// actionStyle bolds the verb / binary name in an output line.
var actionStyle = lipgloss.NewStyle().Bold(true)

// subjectStyle highlights the primary identifier (version, addr,
// busid) so the operator's eye lands on it.
var subjectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fd7ff"))

// dimStyle de-emphasises trailing metadata (commit, build date,
// runtime) so the version line reads like a header with footnotes.
var dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#878787"))
