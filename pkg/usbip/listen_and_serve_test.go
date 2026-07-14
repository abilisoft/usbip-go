// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"context"
	"errors"
	"net"
	"sync"
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

type lifecycleBlockingListener struct {
	acceptStarted chan struct{}
	closed        chan struct{}
	acceptOnce    sync.Once
	closeOnce     sync.Once
}

func newLifecycleBlockingListener() *lifecycleBlockingListener {
	return &lifecycleBlockingListener{
		acceptStarted: make(chan struct{}),
		closed:        make(chan struct{}),
	}
}

func (l *lifecycleBlockingListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.acceptStarted) })
	<-l.closed

	return nil, net.ErrClosed
}

func (l *lifecycleBlockingListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })

	return nil
}

func (*lifecycleBlockingListener) Addr() net.Addr { return lifecycleTestAddr("blocked") }

type lifecycleTestAddr string

func (a lifecycleTestAddr) Network() string { return "test" }
func (a lifecycleTestAddr) String() string  { return string(a) }

// TestExporterListenAndServeUsesTransport asserts the public
// ListenAndServe method dispatches through Transport.Listen with the
// configured TransportOptions. The stubTransport returns a sentinel
// so the assertion focuses on the dispatch contract; downstream
// Serve is exercised by the loopback test below.
func TestExporterListenAndServeUsesTransport(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)

	wantOpts := usbip.TransportOptions{
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
	require.Equal(t, netopts.TransportOptions{
		DialConnectTimeout: 7 * time.Second,
		TCPKeepAliveProbes: 4,
		ReceiveBufferBytes: 128 * 1024,
	}, gotOpts,
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
		usbip.TransportOptions{},
	)

	err := exp.ListenAndServe(t.Context(), "127.0.0.1:0")
	require.ErrorIs(t, err, errStubListenSentinel)
}

func TestExporterListenAndServeRejectsShutdownBeforeBind(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)
	listenCalls := 0

	s.trans.listenFn = func(
		_ context.Context,
		_ string,
		_ internalapp.TransportOptions,
	) (net.Listener, error) {
		listenCalls++

		return nil, errStubListenSentinel
	}

	exp := usbip.NewExporterFromInternalForTestWithTransportOptions(
		s.inner, s.trans, usbip.TransportOptions{},
	)
	require.NoError(t, exp.Shutdown(t.Context()))

	err := exp.ListenAndServe(t.Context(), "127.0.0.1:0")
	require.ErrorIs(t, err, usbip.ErrExporterShutdown)
	require.Zero(t, listenCalls, "terminal lifecycle rejection must precede bind")
}

func TestExporterListenAndServeRejectsOverlapBeforeBind(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)
	listener := newLifecycleBlockingListener()
	listenCalls := 0

	s.trans.listenFn = func(
		_ context.Context,
		_ string,
		_ internalapp.TransportOptions,
	) (net.Listener, error) {
		listenCalls++

		return nil, errStubListenSentinel
	}

	exp := usbip.NewExporterFromInternalForTestWithTransportOptions(
		s.inner, s.trans, usbip.TransportOptions{},
	)
	serveCtx, cancelServe := context.WithCancel(t.Context())
	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(serveCtx, listener) }()

	<-listener.acceptStarted

	err := exp.ListenAndServe(t.Context(), "127.0.0.1:0")
	require.ErrorIs(t, err, usbip.ErrServeAlreadyRunning)
	require.Zero(t, listenCalls, "overlap rejection must precede bind")

	cancelServe()
	require.NoError(t, <-serveDone)
	require.NoError(t, exp.Shutdown(t.Context()))
}

// closeRecordingListener wraps a net.Listener and records the number
// of Close calls it observes. The wrap is identity for Accept/Addr; a
// counter exposes whether ListenAndServe closed the listener on its
// way out, which is the contract being asserted by
// TestExporterListenAndServeClosesListenerOnServeReturn below.
type closeRecordingListener struct {
	net.Listener

	closes int
}

// Close increments the counter and forwards to the wrapped listener.
func (l *closeRecordingListener) Close() error {
	l.closes++

	return l.Listener.Close() //nolint:wrapcheck // test fixture: pass-through
}

// TestExporterListenAndServeClosesListenerOnServeReturn locks in the
// no-listener-leak invariant: when Serve returns (whether on ctx
// cancellation, a startServing rejection, or a permanent listener
// error), the listener that ListenAndServe bound must be closed
// before the call returns. The transport adapter's ctxListener
// closes on ctx cancellation, but a Serve early-return path might
// exit before any ctx signal lands; ListenAndServe must close the
// listener itself in that window.
func TestExporterListenAndServeClosesListenerOnServeReturn(t *testing.T) {
	t.Parallel()

	s := newInternalExporterForTest(t)
	wrapped := &closeRecordingListener{Listener: newLifecycleBlockingListener()}

	s.trans.listenFn = func(
		_ context.Context,
		_ string,
		_ internalapp.TransportOptions,
	) (net.Listener, error) {
		return wrapped, nil
	}

	exp := usbip.NewExporterFromInternalForTestWithTransportOptions(
		s.inner, s.trans,
		usbip.TransportOptions{},
	)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// Serve returns nil on graceful ctx cancellation (the daemon-style
	// shutdown path) and a wrapped error on permanent listener
	// failure. Either way, ListenAndServe must close the listener; the
	// test asserts the close count, not the error shape.
	_ = exp.ListenAndServe(ctx, "127.0.0.1:0")

	require.GreaterOrEqual(t, wrapped.closes, 1,
		"ListenAndServe must close the listener on Serve return")
}
