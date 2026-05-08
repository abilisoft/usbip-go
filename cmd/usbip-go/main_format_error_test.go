// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestRenderMainErrorUsesFormatError pins the main-entrypoint contract
// that errors reach stderr via FormatError, not as the raw wrapped
// error from runCtx. v1 contract §7.4 defines a stable stderr template per
// sentinel; printing the raw error string leaks wrapping prefixes
// ("attach: usbip: device not found: 1-1.2") and breaks operators
// grepping against the spec wording.
func TestRenderMainErrorUsesFormatError(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("attach: %w", usbip.ErrDeviceNotFound)

	var buf bytes.Buffer

	renderMainError(&buf, wrapped)

	require.Contains(t, buf.String(), "usbip-go: device not found",
		"stderr must carry the FormatError template, not the raw wrap chain")
	require.NotContains(t, buf.String(), "attach: usbip: device not found",
		"stderr must not prepend the raw wrap prefix")
}
