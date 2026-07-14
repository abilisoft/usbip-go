// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package usbip_test

import (
	"math"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

func TestConstructorsNonLinuxRejectOtherwiseValidOptionsAsUnsupported(t *testing.T) {
	t.Parallel()

	_, defaultImporterErr := usbip.NewImporter()
	require.ErrorIs(t, defaultImporterErr, usbip.ErrKernelModuleMissing)

	_, defaultExporterErr := usbip.NewExporter()
	require.ErrorIs(t, defaultExporterErr, usbip.ErrKernelModuleMissing)

	_, importerErr := usbip.NewImporter(
		nil,
		usbip.WithImporterTransportOptions(usbip.TransportOptions{
			DialConnectTimeout: time.Second,
		}),
	)
	require.ErrorIs(t, importerErr, usbip.ErrKernelModuleMissing)
	require.NotErrorIs(t, importerErr, usbip.ErrTransportOptionsInvalid)

	_, exporterErr := usbip.NewExporter(
		nil,
		usbip.WithExporterTransportOptions(usbip.TransportOptions{
			ReadDeadline: time.Second,
		}),
		usbip.WithExporterAllowCIDR("127.0.0.0/8"),
		usbip.WithExporterAcceptRateLimit(0),
	)
	require.ErrorIs(t, exporterErr, usbip.ErrKernelModuleMissing)
	require.NotErrorIs(t, exporterErr, usbip.ErrTransportOptionsInvalid)
	require.NotErrorIs(t, exporterErr, usbip.ErrACLInvalid)
	require.NotErrorIs(t, exporterErr, usbip.ErrAcceptRateLimitInvalid)
}

func TestNewImporterNonLinuxRejectsTransportBeforePlatform(t *testing.T) {
	t.Parallel()

	_, err := usbip.NewImporter(usbip.WithImporterTransportOptions(usbip.TransportOptions{
		TCPKeepAliveProbes: -1,
	}))
	require.ErrorIs(t, err, usbip.ErrTransportOptionsInvalid)
	require.NotErrorIs(t, err, usbip.ErrKernelModuleMissing)
}

func TestNewExporterNonLinuxRejectsTransportBeforePlatform(t *testing.T) {
	t.Parallel()

	_, err := usbip.NewExporter(usbip.WithExporterTransportOptions(usbip.TransportOptions{
		SendBufferBytes: -1,
	}))
	require.ErrorIs(t, err, usbip.ErrTransportOptionsInvalid)
	require.NotErrorIs(t, err, usbip.ErrKernelModuleMissing)
}

func TestNewExporterNonLinuxRejectsACLBeforePlatform(t *testing.T) {
	t.Parallel()

	_, err := usbip.NewExporter(usbip.WithExporterAllowCIDR("not-a-cidr"))
	require.ErrorIs(t, err, usbip.ErrACLInvalid)
	require.NotErrorIs(t, err, usbip.ErrKernelModuleMissing)
}

func TestNewExporterNonLinuxRejectsAcceptRateBeforePlatform(t *testing.T) {
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

			_, err := usbip.NewExporter(usbip.WithExporterAcceptRateLimit(tt.rate))
			require.ErrorIs(t, err, usbip.ErrAcceptRateLimitInvalid)
			require.NotErrorIs(t, err, usbip.ErrKernelModuleMissing)
		})
	}
}
