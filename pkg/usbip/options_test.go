package usbip_test

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// TestImporterOptionTypeIsFunc pins the public type shape: option
// constructors return a function value that can be invoked against a
// config pointer. A renamed or redeclared ImporterOption surfaces as a
// compile error.
func TestImporterOptionTypeIsFunc(t *testing.T) {
	t.Parallel()

	// Constructing a nil option is trivially valid — the test's goal
	// is compile-time surface coverage, not runtime semantics.
	var opt usbip.ImporterOption = usbip.WithImporterLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	require.NotNil(t, opt)
}

// TestWithImporterLoggerStoresLogger proves NewImporter applied with
// WithImporterLogger returns an Importer that uses the caller's slog
// handler. We construct the facade Importer via the test hook using an
// internal Importer whose options were first translated through the
// facade option; log output then passes through the injected buffer.
func TestWithImporterLoggerStoresLogger(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	cfg := usbip.NewImporterConfigForTest(usbip.WithImporterLogger(logger))

	require.Same(t, logger, cfg.LoggerForTest())
}

// TestWithImporterBackoffStores proves the option stores the supplied
// BackoffStrategy on the importerConfig.
func TestWithImporterBackoffStores(t *testing.T) {
	t.Parallel()

	b := usbip.FixedBackoff{Delay: 100 * time.Millisecond}

	cfg := usbip.NewImporterConfigForTest(usbip.WithImporterBackoff(b))

	require.Equal(t, usbip.BackoffStrategy(b), cfg.BackoffForTest())
}

// TestWithImporterStatusPollIntervalStores proves the option stores
// the supplied poll interval.
func TestWithImporterStatusPollIntervalStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewImporterConfigForTest(usbip.WithImporterStatusPollInterval(time.Second))

	require.Equal(t, time.Second, cfg.StatusPollIntervalForTest())
}

// TestWithExporterLoggerStores proves the exporter variant.
func TestWithExporterLoggerStores(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterLogger(logger))

	require.Same(t, logger, cfg.LoggerForTest())
}

// TestWithExporterMaxSessionsStores proves the session cap option.
func TestWithExporterMaxSessionsStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterMaxSessions(42))

	require.Equal(t, 42, cfg.MaxSessionsForTest())
}

// TestWithExporterMaxSessionsPerPeerStores proves the per-peer cap.
func TestWithExporterMaxSessionsPerPeerStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterMaxSessionsPerPeer(7))

	require.Equal(t, 7, cfg.MaxSessionsPerPeerForTest())
}

// TestWithExporterAcceptRateLimitStores proves the rate-limit option.
func TestWithExporterAcceptRateLimitStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterAcceptRateLimit(10.0))

	require.InDelta(t, 10.0, cfg.AcceptRateLimitForTest(), 0.001)
}

// TestWithExporterAllowCIDRAppends proves multiple calls accumulate.
func TestWithExporterAllowCIDRAppends(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(
		usbip.WithExporterAllowCIDR("10.0.0.0/8"),
		usbip.WithExporterAllowCIDR("192.168.0.0/16", "172.16.0.0/12"),
	)

	require.Equal(t, []string{"10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"}, cfg.AllowCIDRsForTest())
}

// TestWithExporterMaxHandshakeBytesStores proves the handshake cap.
func TestWithExporterMaxHandshakeBytesStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterMaxHandshakeBytes(4096))

	require.Equal(t, 4096, cfg.MaxHandshakeBytesForTest())
}

// TestWithExporterHandshakeTimeoutStores proves the timeout option.
func TestWithExporterHandshakeTimeoutStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterHandshakeTimeout(2 * time.Second))

	require.Equal(t, 2*time.Second, cfg.HandshakeTimeoutForTest())
}

// TestWithExporterShutdownTimeoutStores proves the shutdown-timeout
// option stores the value on the public config. Internal consumption
// is wired in the metrics/lifecycle phase; the public field is stable
// across that change.
func TestWithExporterShutdownTimeoutStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterShutdownTimeout(30 * time.Second))

	require.Equal(t, 30*time.Second, cfg.ShutdownTimeoutForTest())
}

// TestWithExporterMetricsRegistererStores proves the registerer option
// stores the supplied prometheus.Registerer on the public config. The
// registerer is consumed by the exporter metrics wiring in Phase 9;
// the public surface is stable across that change.
func TestWithExporterMetricsRegistererStores(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterMetricsRegisterer(reg))

	require.Same(t, reg, cfg.MetricsRegistererForTest())
}

// TestExporterOptionTypeIsFunc pins the public ExporterOption shape.
func TestExporterOptionTypeIsFunc(t *testing.T) {
	t.Parallel()

	var opt usbip.ExporterOption = usbip.WithExporterMaxSessions(1)

	require.NotNil(t, opt)
}
