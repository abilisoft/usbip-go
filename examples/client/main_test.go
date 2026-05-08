// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunRejectsInvalidBusID pins the example's argument validation:
// a malformed bus id must be rejected at ParseBusID before any
// kernel-touching operation. Without this gate, `go test ./...` would
// not compile the example and an API drift between pkg/usbip and the
// example main.go could ship without a CI signal.
func TestRunRejectsInvalidBusID(t *testing.T) {
	t.Parallel()

	err := run("10.0.0.5:3240", "definitely-not-a-busid")
	require.Error(t, err, "invalid busid must be rejected")
	require.Contains(t, err.Error(), "parse busid",
		"expected ParseBusID error context, got %q", err.Error())
}

// TestRunRejectsInvalidRemote pins parse-time validation of the host
// argument. ParseRemote runs before ParseBusID, so an invalid host
// surfaces first regardless of busid validity.
func TestRunRejectsInvalidRemote(t *testing.T) {
	t.Parallel()

	err := run("not a host", "1-1.2")
	require.Error(t, err, "invalid host must be rejected")
	require.Contains(t, err.Error(), "parse remote",
		"expected ParseRemote error context, got %q", err.Error())
}
