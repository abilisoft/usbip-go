// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunRejectsInvalidRemote pins parse-time validation of the host
// argument. The example must surface the parse error without
// touching the kernel module probe path.
func TestRunRejectsInvalidRemote(t *testing.T) {
	t.Parallel()

	err := run("not a host", "1-1.2")
	require.Error(t, err, "invalid host must be rejected")
	require.Contains(t, err.Error(), "parse remote",
		"expected ParseRemote error context, got %q", err.Error())
}

// TestRunRejectsInvalidBusID pins ParseBusID validation. ParseRemote
// runs first; a syntactically valid host with an invalid busid hits
// the busid check.
func TestRunRejectsInvalidBusID(t *testing.T) {
	t.Parallel()

	err := run("10.0.0.5:3240", "definitely-not-a-busid")
	require.Error(t, err, "invalid busid must be rejected")
	require.Contains(t, err.Error(), "parse busid",
		"expected ParseBusID error context, got %q", err.Error())
}
