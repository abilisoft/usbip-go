// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"testing"
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/netopts"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestTransportOptionsPreservesV1AliasIdentity locks the published v1 type
// identity so an internal ownership cleanup cannot silently break consumers.
func TestTransportOptionsPreservesV1AliasIdentity(t *testing.T) {
	t.Parallel()

	pub := usbip.TransportOptions{
		DialConnectTimeout: 7 * time.Second,
		TCPKeepAliveProbes: 6,
	}

	require.IsType(t, netopts.TransportOptions{}, pub)
	require.Equal(t, 7*time.Second, pub.DialConnectTimeout)
	require.Equal(t, 6, pub.TCPKeepAliveProbes)
}

// TestWithImporterTransportOptionsRoundTripsToInternal asserts the
// public option function captures the supplied struct and forwards it
// to internal/app's WithImporterTransportOptions when the facade
// translates options. Uses the test-only ImporterConfigForTest export
// to inspect the resulting internal option list.
func TestWithImporterTransportOptionsRoundTripsToInternal(t *testing.T) {
	t.Parallel()

	want := usbip.TransportOptions{
		DialConnectTimeout:   10 * time.Second,
		TCPKeepAliveIdle:     30 * time.Second,
		TCPKeepAliveInterval: 10 * time.Second,
		TCPKeepAliveProbes:   6,
		SendBufferBytes:      256 * 1024,
		ReceiveBufferBytes:   256 * 1024,
		ReadDeadline:         60 * time.Second,
		WriteDeadline:        60 * time.Second,
	}

	cfg := usbip.NewImporterConfigForTest(usbip.WithImporterTransportOptions(want))

	require.Equal(t, want, cfg.TransportOptions())
}

// TestWithExporterTransportOptionsRoundTripsToInternal mirrors the
// importer-side test for the exporter facade option.
func TestWithExporterTransportOptionsRoundTripsToInternal(t *testing.T) {
	t.Parallel()

	want := usbip.TransportOptions{
		DialConnectTimeout: 5 * time.Second,
		ReadDeadline:       30 * time.Second,
	}

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterTransportOptions(want))

	require.Equal(t, want, cfg.TransportOptions())
}

// TestNewImporterRejectsNegativeTransportOptions asserts the public
// fallible constructor surfaces invalid TransportOptions as an error
// (not a panic) by way of the existing internal-app validation
// wrapping. errors.Is must recognise the public sentinel.
func TestNewImporterRejectsNegativeTransportOptions(t *testing.T) {
	t.Parallel()

	_, err := usbip.NewImporter(usbip.WithImporterTransportOptions(usbip.TransportOptions{
		TCPKeepAliveProbes: -1,
	}))
	require.Error(t, err)
	require.ErrorIs(t, err, usbip.ErrTransportOptionsInvalid)
	require.NotErrorIs(t, err, internalapp.ErrTransportOptionsInvalid)
}

// TestNewExporterRejectsNegativeTransportOptions mirrors the importer
// test for the exporter constructor.
func TestNewExporterRejectsNegativeTransportOptions(t *testing.T) {
	t.Parallel()

	_, err := usbip.NewExporter(usbip.WithExporterTransportOptions(usbip.TransportOptions{
		SendBufferBytes: -1,
	}))
	require.Error(t, err)
	require.ErrorIs(t, err, usbip.ErrTransportOptionsInvalid)
	require.NotErrorIs(t, err, internalapp.ErrTransportOptionsInvalid)
}
