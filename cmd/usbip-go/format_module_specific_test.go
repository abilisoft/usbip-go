// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestFormatError_KernelModuleMissingNamesTheActualModule pins
// that the stderr template surfaces the SPECIFIC module the kernel
// adapter said was missing, not a static three-module list.
//
// The kernel adapter's checkModules wraps ErrKernelModuleMissing
// with text like "run `sudo modprobe vhci_hcd`" — a precise hint
// for whichever role (importer/exporter) the caller was running.
// FormatError must propagate that detail so the operator sees
// exactly which modprobe to run, not a kitchen-sink suggestion
// that includes modules they do not need (vhci_hcd on a server,
// usbip_host on a client).
func TestFormatError_KernelModuleMissingNamesTheActualModule(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		wrappedDetail string
		mustContain   string
		mustNotEqual  string
	}{
		{
			name:          "importer_missing_vhci_hcd",
			wrappedDetail: "run `sudo modprobe vhci_hcd`",
			mustContain:   testVHCIHCDModule,
			mustNotEqual:  "usbip-go: kernel module not loaded. Try: sudo modprobe usbip_core vhci_hcd usbip_host",
		},
		{
			name:          "exporter_missing_usbip_host",
			wrappedDetail: "run `sudo modprobe usbip_host`",
			mustContain:   testUSBIPHostModule,
			mustNotEqual:  "usbip-go: kernel module not loaded. Try: sudo modprobe usbip_core vhci_hcd usbip_host",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wrapped := fmt.Errorf("%w: %s", domain.ErrKernelModuleMissing, tc.wrappedDetail)
			require.ErrorIs(t, wrapped, domain.ErrKernelModuleMissing,
				"sanity: wrapped error must still match the sentinel")

			got := FormatError(wrapped)

			require.Contains(t, got, tc.mustContain,
				"FormatError must surface the specific module name from the wrapped detail; got: %q", got)
			require.NotEqual(t, tc.mustNotEqual, got,
				"FormatError must NOT collapse to the static kitchen-sink template; got: %q", got)
		})
	}
}
