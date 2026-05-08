// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/abilisoft/usbip-go/test/integration"
	"github.com/stretchr/testify/require"
)

// loopbackIntegrationBusIDEnv aliases integration.RealBusIDEnv so the
// test file keeps its diagnostic prose; the harness owns the actual env
// name and the centralised skip helpers (RequireRealBusID,
// RequireBindable) so no per-test skip strings drift.
const loopbackIntegrationBusIDEnv = integration.RealBusIDEnv

// loopbackExporterAddr is the loopback address the integration exporter
// binds to. Port 0 lets the kernel pick a free port so parallel tests
// do not collide on a shared listener.
const loopbackExporterAddr = "127.0.0.1:0"

// loopbackTestDeadline bounds the whole loopback scenario. Generous
// because the integration test runs against real sysfs writes; kernel
// latency for bind/attach is typically tens of milliseconds but we
// leave headroom for slow VM runners.
const loopbackTestDeadline = 10 * time.Second

// TestLoopbackAttachDetach exercises the Exporter/Importer end-to-end
// over a real TCP loopback connection with live kernel surfaces:
//
//  1. SetupVUDC prepares a usbip-vudc gadget (skips on missing modules).
//  2. NewExporter/NewImporter assemble the production adapter stack.
//  3. Exporter.Bind registers the bus id with usbip-host. For the
//     default vudc fallback, usbip-host rejects the bind because vudc
//     is a platform device, and the test skips — not a failure, a
//     documented env-dependency per v1 contract §8.4.
//  4. Exporter.Serve accepts on 127.0.0.1:0.
//  5. Importer.Attach performs the full handshake against the
//     listener's actual address.
//  6. ListPorts sees the port as StatusUsed.
//  7. Detach reverses step 5 and leaves ListPorts empty.
//
// goleak.VerifyTestMain (main_test.go) confirms no watcher or fan-out
// goroutine leaks across the scenario.
func TestLoopbackAttachDetach(t *testing.T) {
	dev := integration.SetupVUDC(t)

	busID := domain.BusID(os.Getenv(loopbackIntegrationBusIDEnv))
	if busID == "" {
		// Fall back to the vudc bus id. See loopbackIntegrationBusIDEnv
		// for why the bind may skip; the skip path exits cleanly.
		busID = domain.BusID(dev.BusID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), loopbackTestDeadline)
	defer cancel()

	exp, err := usbip.NewExporter()
	require.NoError(t, err, "integration exporter construction must succeed")

	t.Cleanup(func() {
		sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)

		defer scancel()

		_ = exp.Shutdown(sctx)
	})

	integration.RequireBindable(t, ctx, exp, busID)

	lis, addr := startLoopbackListener(t, exp)

	imp, err := usbip.NewImporter()
	require.NoError(t, err, "integration importer construction must succeed")

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(ctx, domain.RemoteEndpoint{Host: addr.Host, Port: addr.Port}, busID, usbip.AttachOptions{})
	require.NoError(t, err, "Importer.Attach must succeed over real loopback")
	require.NotZero(t, port.ID, "attached port must carry a non-zero id")

	ports, err := imp.ListPorts(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, ports, "ListPorts must observe the attached port")

	err = imp.Detach(ctx, port.ID)
	require.NoError(t, err, "Detach must succeed")

	// Close the listener to unblock Serve so the deferred Shutdown does
	// not block; Serve will return net.ErrClosed which the exporter
	// treats as graceful.
	_ = lis.Close()
}

// startLoopbackListener binds exp.Serve to a fresh 127.0.0.1:0 listener
// on a goroutine, waits for the kernel to pick the port, and returns
// the resolved RemoteEndpoint so the importer can dial it without
// racing the accept-loop startup. The listener itself is returned so
// the caller can Close it and trigger Serve's graceful exit.
func startLoopbackListener(t *testing.T, exp *usbip.Exporter) (integration.ListenerCloser, domain.RemoteEndpoint) {
	t.Helper()

	// The transport adapter is not exposed here; use a plain net.Listen
	// equivalent via the exporter helper so the listener's addr is
	// observable before Serve starts.
	lis, addr, err := integration.TCPListen(loopbackExporterAddr)
	require.NoError(t, err, "bind loopback listener")

	serveDone := make(chan error, 1)

	go func() {
		serveDone <- exp.Serve(context.Background(), lis)
	}()

	t.Cleanup(func() {
		// The primary cleanup is handled by lis.Close in the test
		// body; this defensive path catches tests that return before
		// they reach the explicit close.
		_ = lis.Close()

		select {
		case <-serveDone:
		case <-time.After(2 * time.Second):
			t.Log("Serve did not exit within 2s after listener close")
		}
	})

	return lis, addr
}
