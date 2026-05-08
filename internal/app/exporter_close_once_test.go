package app_test

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/stretchr/testify/require"
)

// countingConn wraps a net.Conn and counts Close invocations so tests
// can assert the close-once contract. Reads and writes pass through.
type countingConn struct {
	net.Conn

	closes atomic.Int32

	mu       sync.Mutex
	closedCh chan struct{}
}

// newCountingConn wraps c with a close counter. closedCh is closed the
// first time Close returns so tests can synchronise without polling.
func newCountingConn(c net.Conn) *countingConn {
	return &countingConn{Conn: c, closedCh: make(chan struct{})}
}

// Close increments the close counter and forwards to the underlying
// conn. Double-close is observable via closeCount().
func (c *countingConn) Close() error {
	if c.closes.Add(1) == 1 {
		defer func() {
			c.mu.Lock()
			defer c.mu.Unlock()

			select {
			case <-c.closedCh:
			default:
				close(c.closedCh)
			}
		}()
	}

	err := c.Conn.Close()
	if err != nil {
		return &countingConnCloseError{err: err}
	}

	return nil
}

// countingConnCloseError wraps a close error so wrapcheck is satisfied
// without the inner error being hidden.
type countingConnCloseError struct{ err error }

func (e *countingConnCloseError) Error() string { return "counting conn close: " + e.err.Error() }

func (e *countingConnCloseError) Unwrap() error { return e.err }

// closeCount returns the current close-call tally.
func (c *countingConn) closeCount() int { return int(c.closes.Load()) }

// countingListener wraps a pipeListener so every accepted server-side
// conn is a countingConn. The test can then assert close-once on the
// exact conn the handler received.
type countingListener struct {
	*pipeListener

	mu    sync.Mutex
	conns []*countingConn
}

// newCountingListener wraps an internal pipeListener and records every
// accepted server-side conn.
func newCountingListener() *countingListener {
	return &countingListener{pipeListener: newPipeListener()}
}

// Accept wraps the inner Accept result in a countingConn.
func (l *countingListener) Accept() (net.Conn, error) {
	c, err := l.pipeListener.Accept()
	if err != nil {
		return nil, err
	}

	cc := newCountingConn(c)

	l.mu.Lock()
	l.conns = append(l.conns, cc)
	l.mu.Unlock()

	return cc, nil
}

// snapshot returns a copy of the conns accepted so far.
func (l *countingListener) snapshot() []*countingConn {
	l.mu.Lock()
	defer l.mu.Unlock()

	out := make([]*countingConn, len(l.conns))
	copy(out, l.conns)

	return out
}

// TestExporter_HandshakeTimeoutClosesConnExactlyOnce asserts the
// close-once contract on the handshake-timeout path. The timeout
// watcher closes the conn and the handler's deferred cleanup
// (handedOff=false on the decode-error path) would close it again.
// The close is guarded by a sync.Once on the session handle so the
// second Close is a no-op.
func TestExporter_HandshakeTimeoutClosesConnExactlyOnce(t *testing.T) {
	t.Parallel()

	const handshakeTimeout = 100 * time.Millisecond

	clk := testutil.NewFakeClockAt(exporterTestEpoch())

	codec := &ProtocolCodecMock{
		DecodeHeaderFunc: wire.NewCodec().DecodeHeader,
	}

	lis := newCountingListener()

	exp := newExporterForTest(t,
		app.WithExporterKernel(&ExporterKernelMock{}),
		app.WithExporterCodec(codec),
		app.WithExporterClock(clk),
		app.WithExporterHandshakeTimeout(handshakeTimeout),
	)
	t.Cleanup(func() { require.NoError(t, exp.Shutdown(context.Background())) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	// Wait for Accept to complete so the server-side countingConn is
	// wired up.
	require.Eventually(t, func() bool {
		return len(lis.snapshot()) == 1
	}, 2*time.Second, 10*time.Millisecond)

	serverConn := lis.snapshot()[0]

	// Give the handler a moment to park in DecodeHeader before firing
	// the timeout — matches TestExporter_HandshakeTimeout's pattern.
	time.Sleep(20 * time.Millisecond)
	clk.Advance(handshakeTimeout + 10*time.Millisecond)

	// Wait for the close-once path to run.
	select {
	case <-serverConn.closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("server-side conn was never closed within 2s")
	}

	// Drain a brief grace period so any spurious second Close surfaces
	// in the counter before we assert.
	time.Sleep(50 * time.Millisecond)

	require.Equal(t, 1, serverConn.closeCount(),
		"handshake-timeout must close the conn exactly once; "+
			"a second Close indicates the deferred cleanup is not guarded")

	cancel()
	<-serveDone
}
