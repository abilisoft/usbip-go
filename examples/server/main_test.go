// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunRejectsInvalidBusID pins ParseBusID validation. The example's
// run() validates the busid before constructing the Exporter or
// touching the listener, so this gate is independent of kernel state.
func TestRunRejectsInvalidBusID(t *testing.T) {
	t.Parallel()

	err := run("0.0.0.0:0", "definitely-not-a-busid")
	require.Error(t, err, "invalid busid must be rejected")
	require.Contains(t, err.Error(), "parse busid",
		"expected ParseBusID error context, got %q", err.Error())
}
