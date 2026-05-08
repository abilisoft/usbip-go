package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestFormatErrorIsSingleLine pins the spec §7.4 promise that the
// stderr line is exactly one line. errors.Join renders its members
// joined by newlines; formatting a joined chain through FormatError's
// %s verb leaked those newlines into the stderr template, breaking
// operator grep scripts that expect a single line per error.
func TestFormatErrorIsSingleLine(t *testing.T) {
	t.Parallel()

	joined := errors.Join(
		usbip.ErrDeviceNotFound,
		errors.New("secondary context"),
		errors.New("tertiary hint"),
	)

	out := FormatError(joined)
	require.NotContains(t, out, "\n",
		"FormatError output must be a single line; got %q", out)
	require.Equal(t, strings.Count(out, "\n"), 0)
}
