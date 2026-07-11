// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// errStubDial is the sentinel returned by TransportMock dial stubs in
// this file when the test only needs to record the dispatch and does
// not care about the post-dial handshake. Using a typed sentinel keeps
// require.ErrorIs assertions specific instead of matching any error.
var errStubDial = errors.New("stub dial: skip handshake")

// TestTransportOptionsZeroValueIsAllowed locks in the zero-valued struct as
// the inherits-current-behavior baseline. Validation must accept zero.
func TestTransportOptionsZeroValueIsAllowed(t *testing.T) {
	t.Parallel()

	require.NoError(t, app.ValidateTransportOptions(app.TransportOptions{}))
}

// TestTransportOptionsRejectsNegativeFields locks in the validation contract:
// negative durations, probe counts, and buffer sizes are rejected before
// adapters are constructed.
func TestTransportOptionsRejectsNegativeFields(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		opts app.TransportOptions
	}{
		{"DialConnectTimeout", app.TransportOptions{DialConnectTimeout: -1 * time.Nanosecond}},
		{"TCPKeepAliveIdle", app.TransportOptions{TCPKeepAliveIdle: -1 * time.Nanosecond}},
		{"TCPKeepAliveInterval", app.TransportOptions{TCPKeepAliveInterval: -1 * time.Nanosecond}},
		{"TCPKeepAliveProbes", app.TransportOptions{TCPKeepAliveProbes: -1}},
		{"SendBufferBytes", app.TransportOptions{SendBufferBytes: -1}},
		{"ReceiveBufferBytes", app.TransportOptions{ReceiveBufferBytes: -1}},
		{"ReadDeadline", app.TransportOptions{ReadDeadline: -1 * time.Nanosecond}},
		{"WriteDeadline", app.TransportOptions{WriteDeadline: -1 * time.Nanosecond}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := app.ValidateTransportOptions(tc.opts)
			require.Error(t, err)
			require.ErrorIs(t, err, app.ErrTransportOptionsInvalid)
		})
	}
}

// TestImporterListRemotePassesTransportOptions asserts the importer-
// level transport options round-trip through to the Transport.Dial
// call. The fake transport short-circuits the post-dial handshake by
// returning errStubDial; the assertion is on the recorded opts.
func TestImporterListRemotePassesTransportOptions(t *testing.T) {
	t.Parallel()

	want := app.TransportOptions{
		DialConnectTimeout: 7 * time.Second,
		TCPKeepAliveIdle:   30 * time.Second,
		ReadDeadline:       5 * time.Second,
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return nil, errStubDial
		},
	}

	imp := newImporterForTest(
		t,
		app.WithImporterTransport(transport),
		app.WithImporterTransportOptions(want),
	)
	t.Cleanup(func() { _ = imp.Close() })

	_, err := imp.ListRemote(context.Background(), testRemote())
	require.Error(t, err)

	require.Len(t, transport.DialCalls(), 1)
	require.Equal(t, want, transport.DialCalls()[0].Opts)
}

// TestImporterAttachPassesImporterTransportOptions asserts Attach uses
// the importer-level options. Per the latency plan §3, v1.x has no
// per-attach transport override; importer-level is authoritative.
func TestImporterAttachPassesImporterTransportOptions(t *testing.T) {
	t.Parallel()

	want := app.TransportOptions{
		DialConnectTimeout: 3 * time.Second,
		WriteDeadline:      4 * time.Second,
	}
	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return nil, errStubDial
		},
	}

	imp := newImporterForTest(
		t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterTransportOptions(want),
	)
	t.Cleanup(func() { _ = imp.Close() })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.Error(t, err)

	require.GreaterOrEqual(t, len(transport.DialCalls()), 1)
	require.Equal(t, want, transport.DialCalls()[0].Opts)
}

// TestImporterTransportOptionsZeroValuePreservesDialCall asserts the
// default importer dials with a zero-valued options struct. Locks in
// the v1.x backward-compatibility invariant: existing code paths that
// do not configure TransportOptions see no behavior change.
func TestImporterTransportOptionsZeroValuePreservesDialCall(t *testing.T) {
	t.Parallel()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return nil, errStubDial
		},
	}

	imp := newImporterForTest(t, app.WithImporterTransport(transport))
	t.Cleanup(func() { _ = imp.Close() })

	_, _ = imp.ListRemote(context.Background(), testRemote())

	require.Len(t, transport.DialCalls(), 1)
	require.Equal(t, app.TransportOptions{}, transport.DialCalls()[0].Opts)
}

// TestNewImporterPanicsOnInvalidTransportOptions proves the validation
// firing inside the importer constructor — a negative-valued field on
// WithImporterTransportOptions causes NewImporter to panic, matching
// the existing internal missing-dependency convention. The public facade
// translates this validation into its error-returning constructor contract.
func TestNewImporterPanicsOnInvalidTransportOptions(t *testing.T) {
	t.Parallel()

	const wantPanic = "app.NewImporter: TransportOptions invalid: " +
		"netopts: TransportOptions invalid: TCPKeepAliveProbes must not be negative"

	require.PanicsWithError(t, wantPanic,
		func() {
			app.NewImporter(
				app.WithImporterKernel(&ImporterKernelMock{}),
				app.WithImporterEvents(&KernelEventsMock{}),
				app.WithImporterTransport(&TransportMock{}),
				app.WithImporterCodec(&ProtocolCodecMock{}),
				app.WithImporterTransportOptions(app.TransportOptions{TCPKeepAliveProbes: -1}),
			)
		})
}
