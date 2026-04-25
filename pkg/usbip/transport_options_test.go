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

// TestTransportOptionsTypeIsNetoptsAlias asserts the public type is a
// Go alias of the internal netopts.TransportOptions, so a value of the
// public type drops directly into the internal interface contract
// without conversion. The aliased identity is the contract the
// pkg/usbip facade is built on (no shadow struct, no repeated field
// list).
func TestTransportOptionsTypeIsNetoptsAlias(t *testing.T) {
	t.Parallel()

	pub := usbip.TransportOptions{
		DialConnectTimeout: 7 * time.Second,
		TCPKeepAliveProbes: 6,
	}

	// `take` accepts netopts.TransportOptions only. If pub were a
	// defined-type clone of netopts.TransportOptions instead of a Go
	// alias, this call would fail to compile. The compile-time check
	// is the actual contract assertion; the equality assertions below
	// confirm field-level integrity.
	take := func(opts netopts.TransportOptions) (time.Duration, int) {
		return opts.DialConnectTimeout, opts.TCPKeepAliveProbes
	}

	idle, probes := take(pub)
	require.Equal(t, 7*time.Second, idle)
	require.Equal(t, 6, probes)
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
	require.ErrorIs(t, err, internalapp.ErrTransportOptionsInvalid)
}

// TestNewExporterRejectsNegativeTransportOptions mirrors the importer
// test for the exporter constructor.
func TestNewExporterRejectsNegativeTransportOptions(t *testing.T) {
	t.Parallel()

	_, err := usbip.NewExporter(usbip.WithExporterTransportOptions(usbip.TransportOptions{
		SendBufferBytes: -1,
	}))
	require.Error(t, err)
	require.ErrorIs(t, err, internalapp.ErrTransportOptionsInvalid)
}
