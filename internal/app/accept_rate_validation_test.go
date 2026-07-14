// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"math"
	"testing"

	app "github.com/abilisoft/usbip-go/internal/app"
	"github.com/stretchr/testify/require"
)

func TestResolveAcceptRateDistinguishesOmittedAndExplicitZero(t *testing.T) {
	t.Parallel()

	require.InDelta(t, app.DefaultAcceptRateLimitForTest(), app.ResolveAcceptRateForTest(), 0)
	require.Zero(t, app.ResolveAcceptRateForTest(app.WithExporterAcceptRateLimit(0, 0)))
}

func TestValidateAcceptRateLimitRejectsNonFinite(t *testing.T) {
	t.Parallel()

	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		err := app.ValidateAcceptRateLimit(value)
		require.ErrorIs(t, err, app.ErrAcceptRateLimitInvalid)
	}

	for _, value := range []float64{-1, 0, 1} {
		require.NoError(t, app.ValidateAcceptRateLimit(value))
	}
}

func TestNewExporterWithErrorRejectsNonFiniteAcceptRate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rate float64
	}{
		{name: "NaN", rate: math.NaN()},
		{name: "positive infinity", rate: math.Inf(1)},
		{name: "negative infinity", rate: math.Inf(-1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			exporter, err := app.NewExporterWithError(
				app.WithExporterKernel(&ExporterKernelMock{}),
				app.WithExporterEvents(&KernelEventsMock{}),
				app.WithExporterCodec(&ProtocolCodecMock{}),
				app.WithExporterAcceptRateLimit(tt.rate, 1),
			)
			require.Nil(t, exporter)
			require.ErrorIs(t, err, app.ErrAcceptRateLimitInvalid)
		})
	}
}
