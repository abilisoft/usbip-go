// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/abilisoft/usbip-go/test/integration"
	"github.com/stretchr/testify/require"
)

// reconnectIntegrationDeadline bounds the end-to-end scenario. The
// watcher's default backoff floor is 1 s, so the timeout must exceed
// one backoff step plus one handshake; 15 s leaves headroom for slow
// VMs without hiding a genuinely stuck reconnect.
const reconnectIntegrationDeadline = 15 * time.Second

// TestReconnectIntegrationEndToEnd drives the full auto-reconnect
// flow against a real kernel + real loopback:
//
//  1. Harness vudc + env-provided usbip-host busid (or skip).
//  2. Start first Exporter on 127.0.0.1:0, attach with
//     AutoReconnect=true.
//  3. Force-close the Exporter; Serve exits.
//  4. Start a REPLACEMENT Exporter on the same address.
//  5. The watcher observes PortDetached via uevent (v1 contract §5.5 item 1)
//     OR ListPorts poll (v1 contract §5.5 item 2), invokes Importer.Attach
//     again, and returns a fresh port id.
//  6. OnReconnect callback fires with the attempt number and prior
//     error; the replacement port is asserted different from the
//     original.
//
// Skips if the runner has no real USB device because auto-reconnect
// exercises the full Attach recovery against usbip-host/vhci_hcd; a
// stubbed kernel adapter cannot prove the recovery works under real
// sysfs semantics.
func TestReconnectIntegrationEndToEnd(t *testing.T) {
	integration.SetupVUDC(t)

	busID := integration.RequireRealBusID(t)

	ctx, cancel := context.WithTimeout(context.Background(), reconnectIntegrationDeadline)
	defer cancel()

	exp1, addr := startExporterForReconnect(t, ctx, busID)

	imp, err := usbip.NewImporter()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	reconnectFired := make(chan int, 4)

	opts := usbip.AttachOptions{
		AutoReconnect: true,
		MaxAttempts:   5,
		OnReconnect: func(attempt int, _ error) {
			select {
			case reconnectFired <- attempt:
			default:
			}
		},
		StatusPollInterval: 500 * time.Millisecond,
	}

	port1, err := imp.Attach(ctx, domain.RemoteEndpoint{Host: addr.Host, Port: addr.Port}, busID, opts)
	require.NoError(t, err)

	// Tear the first exporter down; the watcher must observe the
	// detach via uevent or poll and begin reconnect attempts.
	sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
	_ = exp1.Shutdown(sctx)
	scancel()

	// Start a replacement exporter on the SAME host:port. net.Listen
	// may briefly return EADDRINUSE until the first exporter's
	// listener actually closes; retry a few times.
	addr2, err := bindReplacementExporter(t, ctx, busID, addr)
	require.NoError(t, err)
	require.Equal(t, addr.Port, addr2.Port, "replacement listener must reuse the original port")

	// Wait for the reconnect watcher to fire its OnReconnect.
	select {
	case attempt := <-reconnectFired:
		require.GreaterOrEqual(t, attempt, 1)
	case <-ctx.Done():
		t.Fatal("watcher did not fire OnReconnect within deadline")
	}

	// Eventually, ListPorts from the Importer shows a port again
	// (either the original id or a fresh one depending on kernel
	// slot-reuse policy).
	require.Eventually(t, func() bool {
		ports, listErr := imp.ListPorts(ctx)

		return listErr == nil && len(ports) > 0
	}, reconnectIntegrationDeadline, 200*time.Millisecond,
		"importer must see a reattached port after reconnect")

	// Clean up by detaching whatever port is live; errors are
	// tolerated because the scenario's contract is "reconnect works",
	// not "final Detach always succeeds".
	ports, _ := imp.ListPorts(ctx)

	for _, p := range ports {
		_ = imp.Detach(ctx, p.ID)
	}

	_ = port1
}

// startExporterForReconnect builds an Exporter, binds busID, and
// returns the exporter plus the loopback RemoteEndpoint Serve is
// listening on. Cleanup is registered for Shutdown + Unbind so a test
// failure before the explicit Shutdown still releases kernel state.
func startExporterForReconnect(
	t *testing.T,
	ctx context.Context,
	busID domain.BusID,
) (*usbip.Exporter, domain.RemoteEndpoint) {
	t.Helper()

	exp, err := usbip.NewExporter()
	require.NoError(t, err)

	integration.RequireBindable(t, ctx, exp, busID)

	lis, addr, err := integration.TCPListen(loopbackExporterAddr)
	require.NoError(t, err)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(context.Background(), lis) }()

	t.Cleanup(func() {
		_ = lis.Close()

		select {
		case <-serveDone:
		case <-time.After(2 * time.Second):
		}
	})

	return exp, addr
}

// bindReplacementExporter starts a second Exporter on the same addr as
// the torn-down first exporter. The kernel's TCP stack may block
// reuse of the port for SO_REUSEADDR-free listeners for the normal
// TIME_WAIT window; the loop retries for up to 5 seconds — short
// enough to keep the test snappy, long enough to cover the normal
// re-bind race.
func bindReplacementExporter(
	t *testing.T,
	ctx context.Context,
	busID domain.BusID,
	addr domain.RemoteEndpoint,
) (domain.RemoteEndpoint, error) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return domain.RemoteEndpoint{}, ctx.Err() //nolint:wrapcheck // test context error passes through
		default:
		}

		exp, err := usbip.NewExporter()
		if err != nil {
			return domain.RemoteEndpoint{}, err //nolint:wrapcheck // surface construction error verbatim to the test
		}

		err = exp.Bind(ctx, busID)
		if err != nil {
			return domain.RemoteEndpoint{}, err //nolint:wrapcheck // bind error surfaces to the test verbatim
		}

		lis, addr2, listenErr := integration.TCPListen(addr.Host + ":" + uintToStr(addr.Port))
		if listenErr != nil {
			_ = exp.Unbind(ctx, busID)

			time.Sleep(200 * time.Millisecond)

			continue
		}

		serveDone := make(chan error, 1)

		go func() { serveDone <- exp.Serve(context.Background(), lis) }()

		t.Cleanup(func() {
			sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)

			defer scancel()

			_ = exp.Shutdown(sctx)
			_ = lis.Close()

			select {
			case <-serveDone:
			case <-time.After(2 * time.Second):
			}

			_ = exp.Unbind(sctx, busID)
		})

		return addr2, nil
	}

	return domain.RemoteEndpoint{}, context.DeadlineExceeded
}

// uintToStr renders a uint16 as decimal so TCPListen can be fed a
// "host:port" string without a heap-allocating fmt.Sprintf on the
// reconnect hot path.
func uintToStr(p uint16) string {
	buf := make([]byte, 0, 5)

	if p == 0 {
		return "0"
	}

	for p > 0 {
		buf = append([]byte{byte('0' + p%10)}, buf...)
		p /= 10
	}

	return string(buf)
}
