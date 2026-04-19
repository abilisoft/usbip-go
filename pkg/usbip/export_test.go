package usbip

import (
	"log/slog"
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/prometheus/client_golang/prometheus"
)

// BackoffToInternalForTest exposes backoffToInternal so backoff_test.go
// can assert the translation paths without duplicating the type-switch.
func BackoffToInternalForTest(b BackoffStrategy) internalapp.BackoffStrategy {
	return backoffToInternal(b)
}

// NewImporterFromInternalForTest wraps an already-constructed internal
// Importer in a public *Importer so facade tests can exercise forwarding
// without exposing adapter injection on the public surface. Consumers
// can never reach this helper — it lives in an _test.go file.
func NewImporterFromInternalForTest(inner *internalapp.Importer) *Importer {
	return &Importer{inner: inner}
}

// NewExporterFromInternalForTest mirrors NewImporterFromInternalForTest
// for the Exporter wrapper.
func NewExporterFromInternalForTest(inner *internalapp.Exporter) *Exporter {
	return &Exporter{inner: inner}
}

// ImporterConfigForTest is the test-only view of importerConfig. It
// exposes each tunable via a getter so options_test.go can assert
// storage without the test suite reaching for unexported fields.
type ImporterConfigForTest struct {
	inner importerConfig
}

// NewImporterConfigForTest applies opts to a zero-value importerConfig
// and returns the resulting view. Tests use this to prove each With*
// option lands on the matching config field.
func NewImporterConfigForTest(opts ...ImporterOption) ImporterConfigForTest {
	cfg := importerConfig{}

	for _, opt := range opts {
		opt(&cfg)
	}

	return ImporterConfigForTest{inner: cfg}
}

// LoggerForTest returns the stored logger (or nil if unset).
func (c ImporterConfigForTest) LoggerForTest() *slog.Logger { return c.inner.logger }

// BackoffForTest returns the stored backoff strategy (or nil if unset).
func (c ImporterConfigForTest) BackoffForTest() BackoffStrategy { return c.inner.backoff }

// StatusPollIntervalForTest returns the stored poll interval.
func (c ImporterConfigForTest) StatusPollIntervalForTest() time.Duration {
	return c.inner.statusPollInterval
}

// ExporterConfigForTest is the test-only view of exporterConfig.
type ExporterConfigForTest struct {
	inner exporterConfig
}

// NewExporterConfigForTest applies opts to a zero-value exporterConfig
// and returns the resulting view.
func NewExporterConfigForTest(opts ...ExporterOption) ExporterConfigForTest {
	cfg := exporterConfig{}

	for _, opt := range opts {
		opt(&cfg)
	}

	return ExporterConfigForTest{inner: cfg}
}

// LoggerForTest returns the stored logger.
func (c ExporterConfigForTest) LoggerForTest() *slog.Logger { return c.inner.logger }

// MaxSessionsForTest returns the stored global session cap.
func (c ExporterConfigForTest) MaxSessionsForTest() int { return c.inner.maxSessions }

// MaxSessionsPerPeerForTest returns the stored per-peer session cap.
func (c ExporterConfigForTest) MaxSessionsPerPeerForTest() int { return c.inner.maxSessionsPerPeer }

// AcceptRateLimitForTest returns the stored rate-limit rps.
func (c ExporterConfigForTest) AcceptRateLimitForTest() float64 { return c.inner.acceptRateLimit }

// AllowCIDRsForTest returns the stored allow-list.
func (c ExporterConfigForTest) AllowCIDRsForTest() []string { return c.inner.allowCIDRs }

// MaxHandshakeBytesForTest returns the stored handshake byte cap.
func (c ExporterConfigForTest) MaxHandshakeBytesForTest() int { return c.inner.maxHandshakeBytes }

// HandshakeTimeoutForTest returns the stored handshake timeout.
func (c ExporterConfigForTest) HandshakeTimeoutForTest() time.Duration {
	return c.inner.handshakeTimeout
}

// ShutdownTimeoutForTest returns the stored shutdown timeout.
func (c ExporterConfigForTest) ShutdownTimeoutForTest() time.Duration {
	return c.inner.shutdownTimeout
}

// MetricsRegistererForTest returns the stored Prometheus registerer.
func (c ExporterConfigForTest) MetricsRegistererForTest() prometheus.Registerer {
	return c.inner.metricsRegisterer
}
