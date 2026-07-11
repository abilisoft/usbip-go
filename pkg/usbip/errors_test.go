// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"errors"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestSentinelsReExportedFromDomain asserts every public-library-api OpenSpec sentinel
// appears as a variable in pkg/usbip and is identical (via errors.Is)
// to the pkg/domain original. Aliasing — not wrapping — is required so
// consumer code can match either form.
func TestSentinelsReExportedFromDomain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		u    error
		orig error
	}{
		{"ErrDeviceNotFound", usbip.ErrDeviceNotFound, domain.ErrDeviceNotFound},
		{"ErrDeviceAlreadyBound", usbip.ErrDeviceAlreadyBound, domain.ErrDeviceAlreadyBound},
		{"ErrDeviceNotBound", usbip.ErrDeviceNotBound, domain.ErrDeviceNotBound},
		{"ErrPortInUse", usbip.ErrPortInUse, domain.ErrPortInUse},
		{"ErrNoFreePort", usbip.ErrNoFreePort, domain.ErrNoFreePort},
		{"ErrProtocolMismatch", usbip.ErrProtocolMismatch, domain.ErrProtocolMismatch},
		{"ErrBusIDInvalid", usbip.ErrBusIDInvalid, domain.ErrBusIDInvalid},
		{"ErrPermission", usbip.ErrPermission, domain.ErrPermission},
		{"ErrKernelModuleMissing", usbip.ErrKernelModuleMissing, domain.ErrKernelModuleMissing},
		{"ErrAlreadyRunning", usbip.ErrAlreadyRunning, domain.ErrAlreadyRunning},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Error(t, tc.u, "usbip.%s must be declared", tc.name)
			require.ErrorIs(t, tc.u, tc.orig,
				"usbip.%s must satisfy errors.Is against domain.%s", tc.name, tc.name)
			require.ErrorIs(t, tc.orig, tc.u,
				"domain.%s must satisfy errors.Is against usbip.%s (identity is bi-directional)",
				tc.name, tc.name)
		})
	}
}

// errPeerContext is a static error used to prove sentinel joining
// preserves errors.Is. err113 forbids ad-hoc errors.New inside test
// bodies.
var errPeerContext = errors.New("peer 192.0.2.1")

// TestSentinelsWrappedPreserveErrorsIs proves that code wrapping a
// usbip sentinel with fmt.Errorf still matches via errors.Is against
// the domain original — the aliasing guarantee carries through the
// wrap.
func TestSentinelsWrappedPreserveErrorsIs(t *testing.T) {
	t.Parallel()

	wrapped := errors.Join(usbip.ErrDeviceNotFound, errPeerContext)

	require.ErrorIs(t, wrapped, domain.ErrDeviceNotFound)
	require.ErrorIs(t, wrapped, usbip.ErrDeviceNotFound)
}
