// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

const (
	delayedTCPChunkSize         = 64 * 1024
	delayedTCPForwardingDelay   = 20 * time.Millisecond
	delayedTCPDiscoveryDeadline = 45 * time.Second
	delayedTCPDialDeadline      = 5 * time.Second
	delayedTCPIODeadline        = 2 * time.Second
	delayedTCPShutdownDeadline  = 2 * time.Second
)

type delayedDirectionCounters struct {
	bytes         atomic.Uint64
	delayedWrites atomic.Uint64
	minHoldNanos  atomic.Int64
}

type delayedDirectionSnapshot struct {
	bytes         uint64
	delayedWrites uint64
	minHold       time.Duration
}

type delayedProxySnapshot struct {
	importerToVUDC delayedDirectionSnapshot
	vudcToImporter delayedDirectionSnapshot
}

// delayedTCPProxy is a single-session, test-only TCP bridge. One pump owns
// each direction, preserving stream order and bounded backpressure while
// holding every successfully forwarded chunk for delay. Both kernels keep
// using these sockets after their USB/IP file-descriptor handoffs.
type delayedTCPProxy struct {
	listener net.Listener
	backend  string
	delay    time.Duration

	stop           chan struct{}
	done           chan struct{}
	cancelDial     context.CancelFunc
	shutdownOnce   sync.Once
	connectionLock sync.Mutex
	front          net.Conn
	back           net.Conn
	runErr         error

	importerToVUDC delayedDirectionCounters
	vudcToImporter delayedDirectionCounters
}

func startDelayedTCPProxy(
	t *testing.T,
	backend *net.TCPAddr,
	delay time.Duration,
) *delayedTCPProxy {
	t.Helper()
	require.Positive(t, delay, "proxy delay must be non-zero")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen for delayed TCP proxy")

	dialContext, cancelDial := context.WithCancel(context.Background())
	p := &delayedTCPProxy{
		listener:   listener,
		backend:    backend.String(),
		delay:      delay,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		cancelDial: cancelDial,
	}

	go p.run(dialContext)

	t.Cleanup(func() {
		if closeErr := p.Close(); closeErr != nil {
			t.Errorf("close delayed TCP proxy: %v", closeErr)
		}
	})

	return p
}

func (p *delayedTCPProxy) run(dialContext context.Context) {
	defer close(p.done)
	defer p.shutdown()

	front, err := p.listener.Accept()
	if err != nil {
		p.recordRunError(normalizeProxyTermination("accept frontend", err))

		return
	}

	// This bridge accepts exactly one Importer session.
	_ = p.listener.Close()

	ctx, cancel := context.WithTimeout(dialContext, delayedTCPDialDeadline)
	back, err := (&net.Dialer{}).DialContext(ctx, "tcp", p.backend)
	cancel()
	if err != nil {
		_ = front.Close()
		p.recordRunError(normalizeProxyTermination("dial backend", err))

		return
	}

	if !p.installConnections(front, back) {
		_ = front.Close()
		_ = back.Close()

		return
	}

	type pumpResult struct {
		name string
		err  error
	}

	results := make(chan pumpResult, 2)

	go func() {
		results <- pumpResult{
			name: "importer to vudc",
			err:  p.pump(back, front, &p.importerToVUDC),
		}
	}()

	go func() {
		results <- pumpResult{
			name: "vudc to importer",
			err:  p.pump(front, back, &p.vudcToImporter),
		}
	}()

	first := <-results
	p.shutdown()
	second := <-results

	p.recordRunError(normalizeProxyTermination(first.name, first.err))
	p.recordRunError(normalizeProxyTermination(second.name, second.err))
}

func (p *delayedTCPProxy) installConnections(front, back net.Conn) bool {
	p.connectionLock.Lock()
	defer p.connectionLock.Unlock()

	select {
	case <-p.stop:
		return false
	default:
		p.front = front
		p.back = back

		return true
	}
}

func (p *delayedTCPProxy) pump(
	dst net.Conn,
	src net.Conn,
	counters *delayedDirectionCounters,
) error {
	buf := make([]byte, delayedTCPChunkSize)
	timer := time.NewTimer(p.delay)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		n, readErr := src.Read(buf)
		if n > 0 {
			holdStarted := time.Now()
			timer.Reset(p.delay)

			select {
			case <-timer.C:
			case <-p.stop:
				return net.ErrClosed
			}

			hold := time.Since(holdStarted)
			if writeErr := writeFull(dst, buf[:n]); writeErr != nil {
				return writeErr
			}

			counters.record(n, hold)
		}

		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}

			return readErr
		}
	}
}

func writeFull(dst io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := dst.Write(payload)
		if n > 0 {
			payload = payload[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrNoProgress
		}
	}

	return nil
}

func (c *delayedDirectionCounters) record(n int, hold time.Duration) {
	c.bytes.Add(uint64(n))
	c.delayedWrites.Add(1)

	holdNanos := hold.Nanoseconds()
	for {
		current := c.minHoldNanos.Load()
		if current != 0 && current <= holdNanos {
			return
		}
		if c.minHoldNanos.CompareAndSwap(current, holdNanos) {
			return
		}
	}
}

func (c *delayedDirectionCounters) snapshot() delayedDirectionSnapshot {
	return delayedDirectionSnapshot{
		bytes:         c.bytes.Load(),
		delayedWrites: c.delayedWrites.Load(),
		minHold:       time.Duration(c.minHoldNanos.Load()),
	}
}

func (p *delayedTCPProxy) snapshot() delayedProxySnapshot {
	return delayedProxySnapshot{
		importerToVUDC: p.importerToVUDC.snapshot(),
		vudcToImporter: p.vudcToImporter.snapshot(),
	}
}

func (p *delayedTCPProxy) endpoint(t *testing.T) domain.RemoteEndpoint {
	t.Helper()

	addr, ok := p.listener.Addr().(*net.TCPAddr)
	require.True(t, ok, "delayed proxy listener address must be TCP")

	return domain.RemoteEndpoint{Host: addr.IP.String(), Port: uint16(addr.Port)}
}

func (p *delayedTCPProxy) finished() bool {
	select {
	case <-p.done:
		return true
	default:
		return false
	}
}

func (p *delayedTCPProxy) shutdown() {
	p.shutdownOnce.Do(func() {
		close(p.stop)
		p.cancelDial()
		_ = p.listener.Close()

		p.connectionLock.Lock()
		front := p.front
		back := p.back
		p.connectionLock.Unlock()

		if front != nil {
			_ = front.Close()
		}
		if back != nil {
			_ = back.Close()
		}
	})
}

func (p *delayedTCPProxy) recordRunError(err error) {
	if err == nil {
		return
	}

	p.connectionLock.Lock()
	defer p.connectionLock.Unlock()

	if p.runErr == nil {
		p.runErr = err
	}
}

func normalizeProxyTermination(operation string, err error) error {
	if err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return nil
	}

	return fmt.Errorf("%s: %w", operation, err)
}

func (p *delayedTCPProxy) Wait(timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-p.done:
		p.connectionLock.Lock()
		defer p.connectionLock.Unlock()

		return p.runErr
	case <-timer.C:
		return fmt.Errorf("delayed TCP proxy did not drain within %s", timeout)
	}
}

func (p *delayedTCPProxy) Close() error {
	p.shutdown()

	return p.Wait(delayedTCPShutdownDeadline)
}

func TestDelayedTCPProxyForwardsBothDirections(t *testing.T) {
	backend, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = backend.Close() })

	backendAddr, ok := backend.Addr().(*net.TCPAddr)
	require.True(t, ok)

	request := []byte("importer-to-vudc")
	reply := []byte("vudc-to-importer")
	backendResult := make(chan struct {
		request []byte
		err     error
	}, 1)

	go func() {
		conn, acceptErr := backend.Accept()
		if acceptErr != nil {
			backendResult <- struct {
				request []byte
				err     error
			}{err: acceptErr}

			return
		}
		defer func() { _ = conn.Close() }()
		if deadlineErr := conn.SetDeadline(time.Now().Add(delayedTCPIODeadline)); deadlineErr != nil {
			backendResult <- struct {
				request []byte
				err     error
			}{err: fmt.Errorf("set backend deadline: %w", deadlineErr)}

			return
		}

		gotRequest := make([]byte, len(request))
		_, readErr := io.ReadFull(conn, gotRequest)
		if readErr == nil {
			readErr = writeFull(conn, reply)
		}

		backendResult <- struct {
			request []byte
			err     error
		}{request: gotRequest, err: readErr}
	}()

	proxy := startDelayedTCPProxy(t, backendAddr, delayedTCPForwardingDelay)
	clientContext, cancelClient := context.WithTimeout(t.Context(), delayedTCPDialDeadline)
	defer cancelClient()

	client, err := (&net.Dialer{}).DialContext(
		clientContext,
		"tcp",
		proxy.endpoint(t).String(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.SetDeadline(time.Now().Add(delayedTCPIODeadline)))

	require.NoError(t, writeFull(client, request))
	gotReply := make([]byte, len(reply))
	_, err = io.ReadFull(client, gotReply)
	require.NoError(t, err)
	require.NoError(t, client.Close())

	var result struct {
		request []byte
		err     error
	}
	select {
	case result = <-backendResult:
	case <-time.After(delayedTCPShutdownDeadline):
		t.Fatal("backend server did not finish within proxy shutdown deadline")
	}
	require.NoError(t, result.err)
	require.Equal(t, request, result.request)
	require.Equal(t, reply, gotReply)
	require.NoError(t, proxy.Wait(delayedTCPShutdownDeadline))

	stats := proxy.snapshot()
	require.Equal(t, uint64(len(request)), stats.importerToVUDC.bytes)
	require.Equal(t, uint64(len(reply)), stats.vudcToImporter.bytes)
	require.Positive(t, stats.importerToVUDC.delayedWrites)
	require.Positive(t, stats.vudcToImporter.delayedWrites)
	require.GreaterOrEqual(t, stats.importerToVUDC.minHold, delayedTCPForwardingDelay)
	require.GreaterOrEqual(t, stats.vudcToImporter.minHold, delayedTCPForwardingDelay)
}

// TestURBDataTransferWithBidirectionalLatency proves the real vhci_hcd and
// usbip_vudc kernels continue their post-handshake URB exchange through a
// fixed-delay path. Directional counter deltas are sampled only after both
// phase-1 handoffs, so they prove subsequent block discovery and payload
// verification continue to exchange delayed URBs in both directions.
func TestURBDataTransferWithBidirectionalLatency(t *testing.T) {
	payload := deterministicPayload(e2ePayloadSize)
	imp := newURBImporter(t, usbip.WithImporterTransportOptions(usbip.TransportOptions{
		DialConnectTimeout: delayedTCPDialDeadline,
		ReadDeadline:       delayedTCPDialDeadline,
		WriteDeadline:      delayedTCPDialDeadline,
	}))

	var proxy *delayedTCPProxy
	var handoffStats delayedProxySnapshot

	h := attachVUDC(t, payload, urbAttachConfig{
		importer: imp,
		endpointFor: func(t *testing.T, backend *net.TCPAddr) domain.RemoteEndpoint {
			proxy = startDelayedTCPProxy(t, backend, delayedTCPForwardingDelay)

			return proxy.endpoint(t)
		},
		afterHandoff: func() {
			handoffStats = proxy.snapshot()
		},
		discoveryLimit: delayedTCPDiscoveryDeadline,
	})

	require.NotNil(t, proxy)
	require.False(t, proxy.finished(), "proxy must remain in the kernel URB path")

	got, err := os.ReadFile(h.blockDev)
	require.NoError(t, err, "read delayed-path block device %s", h.blockDev)
	require.GreaterOrEqual(t, len(got), len(payload))
	require.Equal(t, payload, got[:len(payload)],
		"delayed-path payload must match the planted LUN bytes")

	transferStats := proxy.snapshot()
	require.Greater(t, transferStats.importerToVUDC.bytes, handoffStats.importerToVUDC.bytes,
		"post-handoff importer-to-vudc URB bytes must cross the proxy")
	require.Greater(t, transferStats.vudcToImporter.bytes, handoffStats.vudcToImporter.bytes,
		"post-handoff vudc-to-importer URB bytes must cross the proxy")
	require.Greater(t, transferStats.importerToVUDC.delayedWrites, handoffStats.importerToVUDC.delayedWrites,
		"post-handoff importer-to-vudc writes must receive delay")
	require.Greater(t, transferStats.vudcToImporter.delayedWrites, handoffStats.vudcToImporter.delayedWrites,
		"post-handoff vudc-to-importer writes must receive delay")
	require.GreaterOrEqual(t, transferStats.importerToVUDC.minHold, delayedTCPForwardingDelay)
	require.GreaterOrEqual(t, transferStats.vudcToImporter.minHold, delayedTCPForwardingDelay)
	require.False(t, proxy.finished(), "proxy must stay active through payload verification")

	h.detach(t)
	require.NoError(t, proxy.Wait(delayedTCPShutdownDeadline),
		"proxy pumps must drain after exact-Port Detach")
}
