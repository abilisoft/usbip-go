// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

var (
	errSingleLineSecondary = errors.New("secondary context")
	errSingleLineTertiary  = errors.New("tertiary hint")
)

// TestFormatErrorIsSingleLine pins the v1 contract §7.4 promise that the
// stderr line is exactly one line. errors.Join renders its members
// joined by newlines; formatting a joined chain through FormatError's
// %s verb leaked those newlines into the stderr template, breaking
// operator grep scripts that expect a single line per error.
func TestFormatErrorIsSingleLine(t *testing.T) {
	t.Parallel()

	joined := errors.Join(
		usbip.ErrDeviceNotFound,
		fmt.Errorf("%w", errSingleLineSecondary),
		fmt.Errorf("%w", errSingleLineTertiary),
	)

	out := FormatError(joined)
	require.NotContains(t, out, "\n",
		"FormatError output must be a single line; got %q", out)
	require.Equal(t, 0, strings.Count(out, "\n"))
}
