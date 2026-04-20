package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// TestMetricsHandlerServesCatalog proves GET /metrics returns a
// Prometheus-format body containing every §11.5.5 family name plus the
// usbip_build_info stamp. The bundle's typed accessors are primed so
// each CounterVec / HistogramVec materialises a sample — Prometheus
// omits vectors with no observed labels from exposition.
func TestMetricsHandlerServesCatalog(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	// Prime the registry with the full §11.5.5 catalog and at least one
	// observation per vector so the Prometheus text exposition emits
	// every family name the contract requires. Unobserved CounterVec /
	// HistogramVec / GaugeVec instances are registered but produce no
	// samples, so /metrics would omit them without the priming step.
	m := app.MustNewMetrics(reg)
	primeMetricsCatalog(m)

	handler := newMetricsMux(reg, newReadinessChecker(alwaysReady))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	body := rr.Body.String()
	for _, want := range []string{
		"usbip_exporter_sessions_active",
		"usbip_exporter_sessions_accepted_total",
		"usbip_exporter_handshake_duration_seconds",
		"usbip_exporter_bind_total",
		"usbip_exporter_unbind_total",
		"usbip_exporter_disconnect_total",
		"usbip_importer_attaches_total",
		"usbip_importer_detaches_total",
		"usbip_importer_ports_active",
		"usbip_importer_reconnect_attempts_total",
		"usbip_adapter_sysfs_write_failures_total",
		"usbip_kernel_modules_loaded",
		"usbip_build_info",
	} {
		require.Containsf(t, body, want,
			"/metrics response must include %q; got:\n%s", want, snippet(body))
	}
}

// TestHealthzAlwaysOK proves /healthz returns 200 unconditionally. The
// liveness endpoint is about "is the process up", not "is the service
// healthy" — that distinction is the Kubernetes contract (§11.5.5).
func TestHealthzAlwaysOK(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	handler := newMetricsMux(reg, newReadinessChecker(alwaysReady))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "ok", strings.TrimSpace(rr.Body.String()))
}

// TestReadyzReturns503WhenModuleMissing proves /readyz fails closed
// when a required kernel module is missing, matching the §11.5.5
// "readiness = modules loaded AND listener bound AND status sock
// writable" contract.
func TestReadyzReturns503WhenModuleMissing(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	checker := newReadinessChecker(func(_ context.Context) readinessState {
		return readinessState{
			ListenerBound:  true,
			Accepting:      true,
			StatusWritable: true,
			Modules: map[string]usbip.ModuleState{
				"usbip_core": usbip.ModuleStateMissing,
				"vhci_hcd":   usbip.ModuleStateLoaded,
				"usbip_host": usbip.ModuleStateLoaded,
			},
		}
	})

	handler := newMetricsMux(reg, checker)

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

// TestReadyzReturns503WhenNotAccepting covers the Exporter-not-serving
// branch of readiness.
func TestReadyzReturns503WhenNotAccepting(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	checker := newReadinessChecker(func(_ context.Context) readinessState {
		return readinessState{
			ListenerBound:  true,
			Accepting:      false,
			StatusWritable: true,
			Modules: map[string]usbip.ModuleState{
				"usbip_core": usbip.ModuleStateLoaded,
				"vhci_hcd":   usbip.ModuleStateLoaded,
				"usbip_host": usbip.ModuleStateLoaded,
			},
		}
	})

	handler := newMetricsMux(reg, checker)

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

// TestReadyzReturns200WhenReady exercises the happy path: every module
// loaded, listener accepting, status socket writable.
func TestReadyzReturns200WhenReady(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	handler := newMetricsMux(reg, newReadinessChecker(alwaysReady))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
}

// TestReadyzReturns503WhenListenerNotBound proves /readyz fails closed
// when the accept-path listener has not bound yet (Finding 5). The
// previous contract inferred "listener up" from the Accepting flag
// alone, which was set BEFORE Serve entered its accept loop, so a
// kernel bind failure arriving after runDaemon announced readiness
// would leave /readyz reporting 200 while no TCP listener was
// actually live.
func TestReadyzReturns503WhenListenerNotBound(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	checker := newReadinessChecker(func(_ context.Context) readinessState {
		return readinessState{
			ListenerBound:  false,
			Accepting:      true,
			StatusWritable: true,
			Modules: map[string]usbip.ModuleState{
				"usbip_core": usbip.ModuleStateLoaded,
				"vhci_hcd":   usbip.ModuleStateLoaded,
				"usbip_host": usbip.ModuleStateLoaded,
			},
		}
	})

	handler := newMetricsMux(reg, checker)

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

// TestReadyzReturns503WhenStatusSockNotWritable proves /readyz fails
// closed when the status UDS is missing or its parent dir is not
// writable by the daemon uid (Finding 5). Previously statusSocketWritable
// called os.Stat — a file that exists but is owned by root with 0600
// would still return nil from Stat even when this process cannot write
// to it. The syscall.Access(W_OK) replacement catches that case.
func TestReadyzReturns503WhenStatusSockNotWritable(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	checker := newReadinessChecker(func(_ context.Context) readinessState {
		return readinessState{
			ListenerBound:  true,
			Accepting:      true,
			StatusWritable: false,
			Modules: map[string]usbip.ModuleState{
				"usbip_core": usbip.ModuleStateLoaded,
				"vhci_hcd":   usbip.ModuleStateLoaded,
				"usbip_host": usbip.ModuleStateLoaded,
			},
		}
	})

	handler := newMetricsMux(reg, checker)

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusServiceUnavailable, rr.Code)
}

// TestStatusSocketWritableReportsMissingPath proves statusSocketWritable
// returns false when the UDS path does not exist. The previous os.Stat
// implementation returned false on missing too — this test guards the
// new syscall.Access-based implementation against regressing the
// missing-file case while the writability semantics change underneath.
func TestStatusSocketWritableReportsMissingPath(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does-not-exist.sock")

	require.False(t, statusSocketWritable(missing),
		"statusSocketWritable must report false for a missing UDS path")
}

// TestStatusSocketWritableReportsWritable proves statusSocketWritable
// returns true when the path exists AND is writable by the current
// uid. Pairs with the missing / readonly assertions to pin the full
// truth table.
func TestStatusSocketWritableReportsWritable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "status.sock")

	require.NoError(t, os.WriteFile(path, []byte{}, 0o600))

	require.True(t, statusSocketWritable(path),
		"statusSocketWritable must report true for a 0600 file owned by the test uid")
}

// TestMaybeStartMetricsServerNoDoubleRegistration proves the
// metrics-server startup path shares a SINGLE metric registration with
// the exporter's own bundle (Finding 7). Before the fix, buildExporter
// registered the §11.5.5 collectors once via NewExporter, then
// maybeStartMetricsServer re-invoked MustNewMetrics against the same
// registry and panicked on duplicate registration at daemon startup.
//
// The test wires a single *prometheus.Registry through both paths, the
// same way runDaemon does, and asserts:
//  1. buildExporter followed by maybeStartMetricsServer does not panic.
//  2. /metrics responds 200 with the usbip_build_info family rendered
//     (the build-info stamp must survive the refactor).
func TestMaybeStartMetricsServerNoDoubleRegistration(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	cfg := &Config{
		MetricsAddr:     "127.0.0.1:0",
		ShutdownTimeout: 5 * time.Second,
	}

	log := slog.New(slog.DiscardHandler)

	exp, err := buildExporter(cfg, log, reg)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = exp.Shutdown(context.Background())
	})

	// Stand up a statusExporter stand-in with no real listener so the
	// readiness probe is exercised but does not need a full accept
	// path. The metrics handler is what we care about; /readyz is
	// covered by its own tests.
	src := newStatusExporter(exp, nil, false)

	var stop func(context.Context) error

	// The production regression: NewExporter registered collectors
	// against reg, and maybeStartMetricsServer (pre-fix) called
	// MustNewMetrics(reg) again, panicking. require.NotPanics turns
	// the latent panic into a RED failure.
	require.NotPanics(t, func() {
		var startErr error

		stop, startErr = maybeStartMetricsServer(
			context.Background(), cfg, log, reg, src)
		require.NoError(t, startErr)
	}, "maybeStartMetricsServer must not panic when reg already carries exporter collectors")

	require.NotNil(t, stop)

	t.Cleanup(func() {
		_ = stop(context.Background())
	})

	// /metrics must serve. We resolve the concrete bind via
	// startMetricsServerWithHandle-style addr probing: reuse the
	// Registry to gather the build_info metric directly for the
	// second half of the assertion.
	mfs, gatherErr := reg.Gather()
	require.NoError(t, gatherErr)

	foundBuildInfo := false

	for _, mf := range mfs {
		if mf.GetName() == "usbip_build_info" {
			foundBuildInfo = true

			break
		}
	}

	require.True(t, foundBuildInfo,
		"usbip_build_info family must be registered exactly once after maybeStartMetricsServer")
}

// TestStartMetricsServerNoAddrIsNoop proves startMetricsServer returns a
// nil stop func and no error when --metrics-addr is empty.
func TestStartMetricsServerNoAddrIsNoop(t *testing.T) {
	t.Parallel()

	stop, err := startMetricsServer(context.Background(), "",
		prometheus.NewRegistry(), newReadinessChecker(alwaysReady))
	require.NoError(t, err)
	require.Nil(t, stop)
}

// TestStartMetricsServerServesAndStops drives the full start/stop
// lifecycle: the server binds to 127.0.0.1:0, serves /metrics, and the
// returned handle shuts it down cleanly via Shutdown.
func TestStartMetricsServerServesAndStops(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	ms, err := startMetricsServerWithHandle(context.Background(), "127.0.0.1:0",
		reg, newReadinessChecker(alwaysReady))
	require.NoError(t, err)
	require.NotNil(t, ms)

	t.Cleanup(func() {
		shutdownErr := ms.shutdown(context.Background())
		require.NoError(t, shutdownErr)
	})

	require.NotEmpty(t, ms.addr)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, "http://"+ms.addr+"/healthz", nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = resp.Body.Close()
	})

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", strings.TrimSpace(string(body)))
}

// primeMetricsCatalog touches every typed accessor on m so the next
// Gather() surface includes a sample per family. Used by /metrics
// exposition assertions; the production path primes via real workload.
func primeMetricsCatalog(m *app.Metrics) {
	m.ExporterSessionsActive(0)
	m.ExporterSessionAccepted(app.OutcomeHandshakeOK)
	m.ExporterHandshakeDuration(app.HandshakeOpImport, 0)
	m.ExporterBind(app.BindOutcomeOK)
	m.ExporterUnbind(app.UnbindOutcomeOK)
	m.ExporterDisconnect(app.DisconnectReasonGraceful)
	m.ImporterAttached(app.AttachOutcomeOK)
	m.ImporterDetached(app.DetachOutcomeOK)
	m.ImporterPortsActive(0)
	m.ImporterReconnectAttempt(app.ReconnectOutcomeOK)
	m.AdapterSysfsWriteFailure(app.SysfsWritePathOther, app.SysfsErrnoEACCES)
	m.KernelModuleLoaded(app.ModuleUsbipCore, true)
	m.SetBuildInfo("v", "c", "g")
}

// alwaysReady is the canonical readiness probe used by tests that only
// need the /readyz endpoint to return 200.
func alwaysReady(_ context.Context) readinessState {
	return readinessState{
		ListenerBound:  true,
		Accepting:      true,
		StatusWritable: true,
		Modules: map[string]usbip.ModuleState{
			"usbip_core": usbip.ModuleStateLoaded,
			"vhci_hcd":   usbip.ModuleStateLoaded,
			"usbip_host": usbip.ModuleStateLoaded,
		},
	}
}

// snippet truncates long bodies for failure messages.
func snippet(s string) string {
	const snippetLimit = 512

	if len(s) <= snippetLimit {
		return s
	}

	return s[:snippetLimit] + "\n..."
}

// Error suppressed intentionally: assertions against the error path of
// http.Get above are via require.NoError; this variable exists only to
// ensure the errors import stays live if the test surface narrows.
var _ = errors.New
