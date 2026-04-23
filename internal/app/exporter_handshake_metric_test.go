package app_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

// TestExporter_HandshakeDurationMetricStopsBeforeSessionRuntime pins
// the histogram-scope contract: usbip_exporter_handshake_duration_seconds
// must measure the HANDSHAKE only (header decode + body decode +
// kernel handoff), not the full session lifetime. Observing the
// metric AFTER serveImport returns would fold in waitForSessionEnd's
// runtime — the whole session duration (hundreds of milliseconds to
// hours in production) instead of the intended sub-ms handshake cost.
//
// The test injects a FakeClock and stretches the post-handoff session
// duration by 5 seconds of fake-clock time. The handshake itself
// (pre-ExportOnConn) takes zero fake-clock time, so a correct
// implementation records ~0 seconds on the histogram. A buggy
// implementation records ~5 seconds.
func TestExporter_HandshakeDurationMetricStopsBeforeSessionRuntime(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	metrics := app.MustNewMetrics(reg)
	clk := testutil.NewFakeClockAt(exporterTestEpoch())

	const sessionBusID = domain.BusID("5-1")

	// ExportOnConn returns nil immediately, mirroring the real sysfs
	// write semantics. The test advances the fake clock AFTER the
	// session is registered; the handler must observe its handshake
	// metric sample at the ExportOnConn-return boundary (delta ≈ 0
	// fake-clock seconds). If the observation were deferred until
	// after waitForSessionEnd unwinds the 5-second clock advance
	// would be rolled into the sample.
	kernel := &ExporterKernelMock{
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, id domain.BusID) error {
			require.Equal(t, sessionBusID, id)

			return nil
		},
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	// Dedicated per-subscriber channel so the test can push the
	// detach event deterministically.
	eventsCh := make(chan domain.Event, 1)

	kernelEvents := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return eventsCh, func() {}, nil
		},
	}

	codec := newSessionImportCodec(sessionBusID)

	lis := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 11), Port: 9500})

	exp := app.NewExporter(
		app.WithExporterKernel(kernel),
		app.WithExporterEvents(kernelEvents),
		app.WithExporterTransport(&TransportMock{}),
		app.WithExporterCodec(codec),
		app.WithExporterClock(clk),
		app.WithExporterMetrics(metrics),
	)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	serveDone := make(chan error, 1)

	go func() { serveDone <- exp.Serve(ctx, lis) }()

	client, err := lis.dial(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 1
	}, 2*time.Second, 10*time.Millisecond, "session must register after handshake")

	// Wait (with a bounded deadline) for observeImportHandshakeDuration
	// to land. The handler invokes it immediately after ExportOnConn
	// returns, so the sample lands within a scheduler tick. If the
	// observation were instead deferred to handleConn after
	// waitForSessionEnd, no sample would land here — the Eventually
	// would time out cleanly and the test would proceed; the final
	// assertion would then fail because the clock advance below would
	// be rolled into the deferred observation.
	//
	// This deterministic sync point eliminates the race under -race
	// between "handler schedules observeHandshakeDuration" and "test
	// advances fake clock": the observation is guaranteed to have
	// already landed (sample_count >= 1) before the advance.
	const preAdvanceSettle = 200 * time.Millisecond

	deadline := time.After(preAdvanceSettle)

waitSample:
	for {
		if handshakeImportSampleCount(t, reg) >= 1 {
			break waitSample
		}

		select {
		case <-deadline:
			break waitSample
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Session is live and the handler is parked on
	// waitForSessionEnd. Advance the fake clock by a long,
	// obviously-session-lifetime amount of time. The handler has
	// already observed the metric at the ExportOnConn-return boundary
	// with a sub-ms handshake delta, so the advance below is purely
	// session runtime and does not leak into the sample. If the
	// observation were deferred to handler exit, the advance would
	// instead be rolled into the handshake duration.
	const sessionLifetime = 5 * time.Second

	clk.Advance(sessionLifetime)

	// Signal session end via KernelEvents so the handler unwinds.
	eventsCh <- domain.PortDetachedEvent{
		At:   clk.Now(),
		Port: domain.Port{BusID: sessionBusID},
	}

	require.Eventually(t, func() bool {
		return len(exp.Sessions(context.Background())) == 0
	}, 2*time.Second, 10*time.Millisecond, "session must unregister after detach event")

	// Wait for the metric sample to land — the handshake metric is
	// observed inside serveImport at the handoff boundary, which runs
	// before unregisterSession returns.
	require.Eventually(t, func() bool {
		return handshakeImportSampleCount(t, reg) >= 1
	}, 2*time.Second, 10*time.Millisecond, "handshake metric must land")

	observed := handshakeImportSampleSum(t, reg)

	// The handshake work is "instant" on fake clock — the only way
	// observed is even close to sessionLifetime is if the session
	// runtime was rolled into the measurement. Observed must be far
	// below the session lifetime. Use 1s as a generous upper bound
	// (sessionLifetime is 5s).
	require.Less(t, observed, 1.0,
		"handshake histogram sample (%.3fs) leaked session lifetime (%s) — "+
			"metric must be recorded at the handshake boundary, not after session end",
		observed, sessionLifetime)
}

// handshakeImportSampleCount returns the cumulative sample count for
// the import-labelled handshake histogram, or 0 when no samples have
// landed yet.
func handshakeImportSampleCount(t *testing.T, reg *prometheus.Registry) uint64 {
	t.Helper()

	m := findHandshakeImportMetric(t, reg)
	if m == nil {
		return 0
	}

	return m.GetHistogram().GetSampleCount()
}

// handshakeImportSampleSum returns the cumulative observation sum for
// the import-labelled handshake histogram. The single-sample test uses
// this as the per-observation value.
func handshakeImportSampleSum(t *testing.T, reg *prometheus.Registry) float64 {
	t.Helper()

	m := findHandshakeImportMetric(t, reg)
	if m == nil {
		return 0
	}

	return m.GetHistogram().GetSampleSum()
}

// findHandshakeImportMetric walks the registry for the import-labelled
// usbip_exporter_handshake_duration_seconds row, returning nil if no
// sample has landed yet.
func findHandshakeImportMetric(t *testing.T, reg *prometheus.Registry) *dto.Metric {
	t.Helper()

	fams, err := reg.Gather()
	require.NoError(t, err)

	for _, f := range fams {
		if f.GetName() != "usbip_exporter_handshake_duration_seconds" {
			continue
		}

		for _, m := range f.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "op" && lp.GetValue() == string(app.HandshakeOpImport) {
					return m
				}
			}
		}
	}

	return nil
}
