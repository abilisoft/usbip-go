// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestTableRenderer_Event_RespectsNoColorEnv pins that the watch
// renderer's event line passes through the same colorprofile-aware
// writer as the device/port/session tables. A direct write of
// styled content (without styleWriter) keeps ANSI in the output even
// when NO_COLOR is set, breaking subprocess pipelines that expect
// ANSI-free text.
//
// Subprocesses parsing watch output for log forwarding cannot
// reasonably strip ANSI themselves; the contract is that --no-color /
// NO_COLOR / non-TTY destinations all produce plain text.
func TestTableRenderer_Event_RespectsNoColorEnv(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	ev := domain.PortAttachedEvent{
		At: time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		Port: domain.Port{
			ID:    1,
			BusID: "1-1",
		},
	}

	var out bytes.Buffer

	require.NoError(t, tableRenderer{}.Event(&out, ev))

	got := out.String()
	require.NotContains(t, got, "\x1b[",
		"Event renderer with NO_COLOR=1 must not emit ANSI CSI sequences; got: %q", got)
}
