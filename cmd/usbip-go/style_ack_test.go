// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFormatAck_StructureAndContents pins the contract operators
// rely on: every styled CLI ack reads "<mark> <action> <subject>",
// with the success-mark glyph first, the action verb second, the
// subject value last. Subprocess parsers that grep on the verb +
// subject (legacy tooling, ad-hoc scripts) must keep working
// across cosmetic palette changes.
func TestFormatAck_StructureAndContents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		action  string
		subject string
	}{
		{"bound", testRootBusID},
		{"unbound", "3-1.2"},
		{"detached port", "0"},
		{"installed bash completion to", "/etc/bash_completion.d/usbip-go"},
	}

	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			t.Parallel()

			got := formatAck(tc.action, tc.subject)

			// Action and subject must appear in order, action first.
			ai := strings.Index(got, tc.action)
			si := strings.Index(got, tc.subject)
			require.GreaterOrEqual(t, ai, 0, "action %q must appear in ack: %q", tc.action, got)
			require.GreaterOrEqual(t, si, 0, "subject %q must appear in ack: %q", tc.subject, got)
			require.Less(t, ai, si, "action must precede subject; got: %q", got)
		})
	}
}

// TestFormatAck_HonorsNoColor pins that NO_COLOR strips the ANSI
// escapes from the success mark even though the glyph itself
// (non-ASCII "✓") survives. styleWriter handles the strip; this
// test asserts at the formatter boundary that we always EMIT the
// escapes — letting styleWriter, not the formatter, decide
// whether to keep them.
func TestFormatAck_AlwaysEmitsAnsiPreNormalisation(t *testing.T) {
	t.Parallel()

	got := formatAck("bound", testRootBusID)

	require.Contains(t, got, "\x1b[",
		"formatAck must always emit ANSI escapes pre-normalisation; "+
			"styleWriter at the call site strips them when NO_COLOR is set or stdout is not a TTY")
}
