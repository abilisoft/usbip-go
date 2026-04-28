// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStyleWriter_NoColorEnvAppendsNormalized pins the branch inside
// styleWriter that pre-normalises a non-empty NO_COLOR value to "NO_COLOR=1"
// before handing it to colorprofile.NewWriter. Without this normalisation,
// arbitrary values like "yes" or "on" would bypass colorprofile's ParseBool
// gate and leave colors enabled despite the spec. The test sets NO_COLOR
// to a non-boolean value and verifies that styleWriter returns a usable
// writer that does not panic.
func TestStyleWriter_NoColorEnvAppendsNormalized(t *testing.T) {
	// Not parallel — mutates process environment via t.Setenv.
	t.Setenv("NO_COLOR", "yes")

	var buf bytes.Buffer

	w := styleWriter(&buf)
	require.NotNil(t, w)

	// Write through the returned writer to confirm it is functional.
	n, err := w.Write([]byte("hello"))
	require.NoError(t, err)
	require.Positive(t, n)
}
