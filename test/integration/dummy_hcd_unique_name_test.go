// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/test/integration"
)

// TestDummyHCDGadgetName_IsUniquePerTest pins that the gadget
// configfs directory the harness creates is per-test-unique even
// when callers pass the same logical name. Without per-test
// uniqueness, two concurrent integration runs hitting
// /sys/kernel/config/usb_gadget/<name> race for the same configfs
// directory: the second mkdir EEXIST's, and waitForNewGadgetBusID
// can claim the OTHER run's gadget because its snapshot was taken
// before either gadget enumerated.
//
// We assert the contract at the level of the path the harness picks,
// not the configfs operation, so the test compiles and runs on every
// linux even without dummy_hcd modules. The harness exposes the path
// it would create via GadgetConfigfsPathFor — a thin pure helper
// added solely so this test can verify the property without standing
// up an actual gadget.
func TestDummyHCDGadgetName_IsUniquePerTest(t *testing.T) {
	t.Parallel()

	logical := "shared_logical_name"

	a := integration.GadgetConfigfsPathFor(t, logical)

	// A separate testing.T (simulated via t.Run) must yield a
	// different configfs path even when given the same logical
	// caller-supplied name.
	var b string

	t.Run("peer_subtest", func(sub *testing.T) {
		b = integration.GadgetConfigfsPathFor(sub, logical)
	})

	require.NotEqual(t, a, b,
		"two distinct testing.T values must map the same logical gadget name to DIFFERENT configfs directories so concurrent runs do not race")
	require.Contains(t, filepath.Base(a), strings.ReplaceAll(t.Name(), "/", "_"),
		"chosen configfs name must encode the test name so failures point at the right test")
	require.Contains(t, filepath.Base(b), "peer_subtest",
		"sub-test gadget name must encode the subtest name")
}
