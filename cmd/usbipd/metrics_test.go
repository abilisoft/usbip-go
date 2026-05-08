package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	handler := newMetricsMux(
		reg, newReadinessChecker(alwaysReady), // not yet exported
	)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)

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
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)

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
			Accepting:     true,
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
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

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
			Accepting:     false,
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
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

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
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)

	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
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
// returned stop func shuts it down cleanly.
func TestStartMetricsServerServesAndStops(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	stop, err := startMetricsServer(context.Background(), "127.0.0.1:0",
		reg, newReadinessChecker(alwaysReady))
	require.NoError(t, err)
	require.NotNil(t, stop)

	t.Cleanup(func() {
		shutdownErr := stop(context.Background())
		require.NoError(t, shutdownErr)
	})

	// startMetricsServer exposes the bound addr via the returned handle
	// so tests can hit it without port-scanning. The interface is
	// deliberately narrow: callers outside the server type only observe
	// Shutdown and Addr.
	addr := metricsServerAddr(stop)
	require.NotEmpty(t, addr)

	resp, err := http.Get("http://" + addr + "/healthz") //nolint:noctx,gosec // test-only localhost GET
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = resp.Body.Close()
	})

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "ok", strings.TrimSpace(string(body)))
}

// alwaysReady is the canonical readiness probe used by tests that only
// need the /readyz endpoint to return 200.
func alwaysReady(_ context.Context) readinessState {
	return readinessState{
		Accepting:     true,
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
	const max = 512

	if len(s) <= max {
		return s
	}

	return s[:max] + "\n..."
}

// Error suppressed intentionally: assertions against the error path of
// http.Get above are via require.NoError; this variable exists only to
// ensure the errors import stays live if the test surface narrows.
var _ = errors.New
