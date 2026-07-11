// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMapErrorCanceledIsInterrupt pins the mapping for context.Canceled:
// the user interrupted via SIGINT or a parent cancelled the call
// without a deadline. Exit 9 (timeout) is wrong here because no clock
// expired and the stderr "operation timed out" message misleads the
// operator into hunting a timeout that never existed. The Unix
// convention for SIGINT is 128+signal = 130; this mapping lets users
// distinguish a clean Ctrl-C from a genuine timeout via $? alone.
func TestMapErrorCanceledIsInterrupt(t *testing.T) {
	t.Parallel()

	require.Equal(t, ExitInterrupted, MapError(context.Canceled),
		"context.Canceled must map to ExitInterrupted, not ExitTimeout")
}

// TestFormatErrorCanceledNotTimeout pins the stderr template for
// context.Canceled: it must NOT use the "operation timed out" wording
// reserved for context.DeadlineExceeded. Operators parsing stderr
// against the cli-interface OpenSpec table need the interrupted case to be visually
// distinct.
func TestFormatErrorCanceledNotTimeout(t *testing.T) {
	t.Parallel()

	msg := FormatError(context.Canceled)
	require.NotContains(t, msg, "timed out",
		"context.Canceled stderr must not claim timeout")
	require.Contains(t, msg, "interrupted",
		"context.Canceled stderr should identify interruption")
}
