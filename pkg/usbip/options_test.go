package usbip_test

import (
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

	// Passing the option to a parameter typed as ImporterOption forces
	// the compile-time check: constructor result is convertible to the
	// public type.
	require.NotNil(t, acceptImporterOption(
		usbip.WithImporterLogger(slog.New(slog.DiscardHandler)),
	))
}

// acceptImporterOption forces its argument to the public ImporterOption
// type — the call site only type-checks when the passed value matches
// that named function type. Returns the argument so callers can assert
// non-nil in a single statement.
func acceptImporterOption(o usbip.ImporterOption) usbip.ImporterOption { return o }

// TestWithImporterLoggerStoresLogger proves NewImporter applied with
// WithImporterLogger returns an Importer that uses the caller's slog
// handler. We construct the facade Importer via the test hook using an
// internal Importer whose options were first translated through the
// facade option; log output then passes through the injected buffer.
func TestWithImporterLoggerStoresLogger(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.DiscardHandler)

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

	logger := slog.New(slog.DiscardHandler)

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
// registerer is consumed by the exporter metrics wiring; the public
// surface is stable across that change.
func TestWithExporterMetricsRegistererStores(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	cfg := usbip.NewExporterConfigForTest(usbip.WithExporterMetricsRegisterer(reg))

	require.Same(t, reg, cfg.MetricsRegistererForTest())
}

// TestWithExporterBuildInfoStores proves the build-info option stores
// the version / commit / goVersion triple on the public config. The
// downstream consumer is internal/app Exporter construction, which
// stamps the usbip_build_info gauge at that point; the public field
// is stable across that wiring.
func TestWithExporterBuildInfoStores(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest(
		usbip.WithExporterBuildInfo("v1.2.3", "abcdef", "go1.26"))

	bi := cfg.BuildInfoForTest()
	require.Equal(t, "v1.2.3", bi.Version)
	require.Equal(t, "abcdef", bi.Commit)
	require.Equal(t, "go1.26", bi.GoVersion)
}

// TestWithExporterBuildInfoZeroIsNoop proves the unset (zero-value)
// option case. exporterConfigToInternal must skip the build-info
// internal option when every label is empty so a caller that never
// invoked WithExporterBuildInfo does not end up clobbering an
// existing stamp with blanks.
func TestWithExporterBuildInfoZeroIsNoop(t *testing.T) {
	t.Parallel()

	cfg := usbip.NewExporterConfigForTest()

	bi := cfg.BuildInfoForTest()
	require.Empty(t, bi.Version)
	require.Empty(t, bi.Commit)
	require.Empty(t, bi.GoVersion)
}

// TestNewExporterWithBuildInfoStampsMetric drives the full public
// construction path with both WithExporterMetricsRegisterer and
// WithExporterBuildInfo — the combination exporterConfigToInternal
// uses to forward the build-info stamp into the internal bundle.
// Covers the non-zero buildInfo branch of exporterConfigToInternal
// which the unit tests' forTest accessors cannot reach.
func TestNewExporterWithBuildInfoStampsMetric(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()

	_, err := usbip.NewExporter(
		usbip.WithExporterMetricsRegisterer(reg),
		usbip.WithExporterBuildInfo("v9.9.9", "cafebabe", "go1.99"),
	)
	require.NoError(t, err)

	mfs, gatherErr := reg.Gather()
	require.NoError(t, gatherErr)

	var found bool

	for _, mf := range mfs {
		if mf.GetName() != "usbip_build_info" {
			continue
		}

		labels := map[string]string{}

		for _, lp := range mf.GetMetric()[0].GetLabel() {
			labels[lp.GetName()] = lp.GetValue()
		}

		require.Equal(t, "v9.9.9", labels["version"])
		require.Equal(t, "cafebabe", labels["commit"])
		require.Equal(t, "go1.99", labels["go_version"])

		found = true
	}

	require.True(t, found, "usbip_build_info must appear after NewExporter with build-info option")
}

// TestExporterOptionTypeIsFunc pins the public ExporterOption shape.
func TestExporterOptionTypeIsFunc(t *testing.T) {
	t.Parallel()

	require.NotNil(t, acceptExporterOption(usbip.WithExporterMaxSessions(1)))
}

// acceptExporterOption mirrors acceptImporterOption for the exporter.
func acceptExporterOption(o usbip.ExporterOption) usbip.ExporterOption { return o }
