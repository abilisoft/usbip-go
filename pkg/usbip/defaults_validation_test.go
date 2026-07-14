// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"math"
	"testing"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestNewExporterAllowCIDRInvalidReturnsError proves bad CIDR strings surface
// at construction time before platform adapter availability.
func TestNewExporterAllowCIDRInvalidReturnsError(t *testing.T) {
	t.Parallel()

	_, err := usbip.NewExporter(usbip.WithExporterAllowCIDR("not-a-cidr"))
	require.Error(t, err)
	require.ErrorIs(t, err, usbip.ErrACLInvalid)
	require.ErrorContains(t, err, "construct exporter:")
}

func TestNewExporterRejectsNonFiniteAcceptRate(t *testing.T) {
	t.Parallel()

	for _, rps := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := usbip.NewExporter(usbip.WithExporterAcceptRateLimit(rps))
		require.ErrorIs(t, err, usbip.ErrAcceptRateLimitInvalid)
		require.ErrorContains(t, err, "construct exporter:")
	}
}
