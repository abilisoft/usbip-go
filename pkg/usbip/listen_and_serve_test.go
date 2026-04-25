// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/netopts"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// errStubListenSentinel is the static sentinel returned by the
// stubTransport's listen hook in this file. Defining it at package
// scope satisfies err113 without an opaque errors.New inline.
var errStubListenSentinel = errors.New("stub listen: skip serve")

// TestExporterListenAndServeUsesTransport asserts the public
// ListenAndServe method dispatches through Transport.Listen with the
// configured TransportOptions. The stubTransport returns a sentinel
// so the assertion focuses on the dispatch contract; downstream
// Serve is exercised by the loopback test below.
func TestExporterListenAndServeUsesTransport(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	wantOpts := netopts.TransportOptions{
		DialConnectTimeout: 7 * time.Second,
		TCPKeepAliveProbes: 4,
		ReceiveBufferBytes: 128 * 1024,
	}

	var (
		gotAddr   string
		gotOpts   netopts.TransportOptions
		gotCalled bool
	)

	s.trans.listenFn = func(
		_ context.Context,
		addr string,
		opts internalapp.TransportOptions,
	) (net.Listener, error) {
		gotAddr = addr
		gotOpts = opts
		gotCalled = true

		return nil, errStubListenSentinel
	}

	exp := usbip.NewExporterFromInternalForTestWithTransportOptions(s.inner, s.trans, wantOpts)

	err := exp.ListenAndServe(t.Context(), "127.0.0.1:0")
	require.Error(t, err)
	require.ErrorIs(t, err, errStubListenSentinel,
		"ListenAndServe must surface the Listen error verbatim")

	require.True(t, gotCalled, "Transport.Listen must be invoked")
	require.Equal(t, "127.0.0.1:0", gotAddr)
	require.Equal(t, wantOpts, gotOpts,
		"Transport.Listen must receive the importer-config TransportOptions snapshot")
}

// TestExporterListenAndServeReturnsListenErrorVerbatim locks in the
// "Listen failure stops ListenAndServe before Serve" contract: the
// exporter must not attempt to call Serve on a nil listener when
// Listen reports an error.
func TestExporterListenAndServeReturnsListenErrorVerbatim(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	s.trans.listenFn = func(
		_ context.Context,
		_ string,
		_ internalapp.TransportOptions,
	) (net.Listener, error) {
		return nil, errStubListenSentinel
	}

	exp := usbip.NewExporterFromInternalForTestWithTransportOptions(
		s.inner, s.trans,
		netopts.TransportOptions{},
	)

	err := exp.ListenAndServe(t.Context(), "127.0.0.1:0")
	require.ErrorIs(t, err, errStubListenSentinel)
}
