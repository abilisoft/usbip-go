// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// TestReconnect_PortsGaugeStaysAccurateAcrossReconnect pins the
// gauge-accuracy contract: when a reconnect watcher successfully
// reattaches and the kernel lands the replacement on a different
// PortID than the original slot, the old-port handle is removed from
// the map AND the usbip_importer_ports_active gauge is refreshed so
// operators see the true live-port count.
//
// If finishReconnectSuccess called removeHandle(oldPortID) without
// re-running updateImporterPortsGauge, the gauge observed value would
// drift upward by one on every cross-slot reconnect and only get
// corrected on the next gauge-updating event (another
// Attach/Detach/rollback/Close).
//
// This test drives a first Attach (gauge=1), fires a detach uevent,
// advances the fake clock past the backoff so the watcher reattaches
// onto a new port id (gauge transiently =2 from Attach, old slot
// removed), then asserts gauge ends at 1.
func TestReconnect_PortsGaugeStaysAccurateAcrossReconnect(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)

	imp, clk, events, kernel := newReconnectFixtureWithMetrics(t, metrics)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	var nextID atomic.Uint32

	kernel.AttachRemoteFunc = func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
		return domain.PortID(nextID.Add(1)), nil
	}

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), attachOptionsWithBackoff())
	require.NoError(t, err)
	require.Equal(t, domain.PortID(1), port.ID)

	// readPortsGauge is scoped to this test so the file does not add
	// callers to gaugeOnlyValue that all share a single metric name —
	// unparam would otherwise flag the helper's `name` parameter as
	// a constant literal across the package.
	readPortsGauge := func() float64 {
		t.Helper()

		fams, gatherErr := reg.Gather()
		require.NoError(t, gatherErr)

		for _, f := range fams {
			if f.GetName() != "usbip_importer_ports_active" {
				continue
			}

			ms := f.GetMetric()
			if len(ms) == 0 {
				return 0
			}

			return ms[0].GetGauge().GetValue()
		}

		return 0
	}

	// Baseline: one live port, gauge should be 1.
	require.InDelta(t, 1.0, readPortsGauge(), 0.0001,
		"initial Attach must land gauge=1")

	events.waitFor(t, 1)

	// Fire the detach uevent for our port id, then push backoff forward
	// until the reconnect-driven AttachRemote fires and lands PortID(2).
	events.channel(t, 0) <- domain.PortDetachedEvent{Port: domain.Port{ID: port.ID}}

	require.Eventually(t, func() bool {
		clk.Advance(reconnectTestBackoff().Delay)

		return len(kernel.AttachRemoteCalls()) == 2
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"reconnect should trigger exactly one additional AttachRemote")

	// The replacement watcher subscribes from the successful Attach
	// path; once it is registered, the reconnect bookkeeping
	// (removeHandle for the old slot) is guaranteed to have run.
	events.waitFor(t, 2)

	// The gauge must reflect the true live-port count (1 — the
	// replacement port). A regression that removed the old handle but
	// skipped updateImporterPortsGauge would leave the gauge at 2.
	require.Eventually(t, func() bool {
		return readPortsGauge() == 1.0
	}, reconnectTestSettleBudget, 5*time.Millisecond,
		"ports gauge must equal 1 after cross-slot reconnect; got %v",
		readPortsGauge())
}

// newReconnectFixtureWithMetrics mirrors newReconnectFixture but lets
// the caller supply a *Metrics bundle so gauge-value assertions work
// against a caller-owned Prometheus registry.
func newReconnectFixtureWithMetrics(
	t *testing.T,
	metrics *app.Metrics,
) (*app.Importer, *testutil.FakeClock, *eventChannelRegistry, *ImporterKernelMock) {
	t.Helper()

	registry := newEventChannelRegistry()

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			ch, cancel := registry.subscribe()

			return ch, cancel, nil
		},
	}

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		DetachPortFunc:       func(_ context.Context, _ domain.PortID) error { return nil },
		ListPortsFunc:        func(_ context.Context) ([]domain.Port, error) { return nil, nil },
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	clk := testutil.NewFakeClockAt(importerTestEpoch())

	imp := app.NewImporter(
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(events),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterClock(clk),
		app.WithImporterMetrics(metrics),
	)

	return imp, clk, registry, kernel
}
