// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunRejectsInvalidBusID pins ParseBusID validation. The metrics
// example takes three positional args; the busid is parsed before
// any listener bind or Prometheus registration, so this gate exercises
// arg-validation independent of network state.
func TestRunRejectsInvalidBusID(t *testing.T) {
	t.Parallel()

	err := run("127.0.0.1:0", "127.0.0.1:0", "definitely-not-a-busid")
	require.Error(t, err, "invalid busid must be rejected")
	require.Contains(t, err.Error(), "parse busid",
		"expected ParseBusID error context, got %q", err.Error())
}
