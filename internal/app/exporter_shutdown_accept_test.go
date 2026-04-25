// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/stretchr/testify/require"
)

// TestExporterShutdown_RejectsNewConnections locks in the spec §3.4
// contract (documented on pkg/usbip.Exporter.Shutdown): Shutdown
// "stops accepting new connections". Shutdown closes the listener,
// acceptShouldStop classifies net.ErrClosed as a normal stop, Serve
// returns nil promptly, and any new dial after Shutdown returns
// net.ErrClosed.
//
// The test uses the default exporter fixture (codec is a zero-value
// ProtocolCodecMock so any accepted conn that reaches handleConn
// panics). We therefore DO NOT dial before Shutdown — the assertion is
// purely on the listener-close + Serve-return side. A post-Shutdown
// dial must fail because the listener is closed.
func TestExporterShutdown_RejectsNewConnections(t *testing.T) {
	t.Parallel()

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 42), Port: 9100})

	// DecodeHeader returns EOF so any conn that sneaks past the
	// listener-close gate drops through handleConn without panicking on
	// the zero-value mock. The test's actual assertion is that dial
	// FAILS after Shutdown, but a fallback that survives is defensive.
	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: func(_ io.Reader) (uint16, wire.OpCode, uint32, error) {
			return 0, 0, 0, io.EOF
		},
	}

	exp := newExporterForTest(t, app.WithExporterCodec(codec))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	// Let Serve park on Accept before we call Shutdown.
	time.Sleep(20 * time.Millisecond)

	// Shut down. Per the documented contract, no ctx-cancel first —
	// Shutdown alone must be enough to stop accepts and unwind Serve.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()

	require.NoError(t, exp.Shutdown(shutdownCtx))

	// After Shutdown returns, a new dial must fail: the listener is
	// closed.
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer dialCancel()

	_, dialErr := lis.dial(dialCtx)
	require.Error(t, dialErr, "dial after Shutdown must fail — listener must be closed")
	require.ErrorIs(t, dialErr, net.ErrClosed,
		"dial after Shutdown must report net.ErrClosed")

	// Serve must return promptly: Shutdown closed the listener so
	// acceptLoop unwound via acceptShouldStop.
	select {
	case err := <-serveDone:
		require.NoError(t, err, "Serve should return nil after Shutdown")
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return within 2s of Shutdown — listener was not closed")
	}
}
