// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip

import (
	"log/slog"
	"runtime"
	"sync"
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
)

// ProbeOneAtForTest exposes probeOneAt so tests can point the probe at
// a tmpdir root and assert the Unknown classification on non-ENOENT
// stat failures (e.g. EACCES on a chmod-0 directory). The Linux-only
// probeOneAt lives behind a build tag, so the actual invocation
// happens in probeOneAtForTestInvoke which has matching tags; this
// top-level shim keeps the call sites platform-neutral.
func ProbeOneAtForTest(root, name string) ModuleState {
	if runtime.GOOS != "linux" {
		return ModuleStateUnknown
	}

	return probeOneAtForTestInvoke(root, name)
}

// BackoffToInternalForTest exposes backoffToInternal so backoff_test.go
// can assert the translation paths without duplicating the type-switch.
func BackoffToInternalForTest(b BackoffStrategy) internalapp.BackoffStrategy {
	return backoffToInternal(b, &sync.Mutex{})
}

// TranslateInternalErrForTest exposes the internal→public sentinel
// translator so errors_boundary_test.go can cover the branches that
// forwarding tests cannot reach directly (notably ErrServeAlreadyRunning
// and the pass-through for non-lifecycle errors).
func TranslateInternalErrForTest(err error) error {
	return translateInternalErr(err)
}

// NewImporterFromInternalForTest wraps an already-constructed internal
// Importer in a public *Importer so facade tests can exercise forwarding and
// public options without exposing adapter injection on the public surface.
// Consumers can never reach this helper — it lives in an _test.go file.
func NewImporterFromInternalForTest(
	inner *internalapp.Importer,
	opts ...ImporterOption,
) *Importer {
	cfg := newImporterConfig()

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	return &Importer{inner: inner, cfg: cfg}
}

// ImporterAttachOptionsForTest applies importer defaults and exposes the
// internal translation result so facade tests can verify backoff precedence
// without performing kernel or network side effects.
func ImporterAttachOptionsForTest(
	attach AttachOptions,
	opts ...ImporterOption,
) internalapp.AttachOptions {
	cfg := newImporterConfig()
	for _, opt := range opts {
		opt(&cfg)
	}

	importer := &Importer{cfg: cfg}

	return importer.attachOptionsToInternal(importer.mergeAttachOptions(attach))
}

// NewExporterFromInternalForTest mirrors NewImporterFromInternalForTest
// for the Exporter wrapper.
func NewExporterFromInternalForTest(inner *internalapp.Exporter) *Exporter {
	return &Exporter{inner: inner}
}

// NewExporterFromInternalForTestWithTransportOptions wraps an already-
// constructed internal Exporter in a public *Exporter and seeds the
// public wrapper's transportOptions snapshot plus a transport stub.
// ListenAndServe tests use this seam to inject the same stub transport
// the inner Exporter was built with so a single Listen call can be
// observed end-to-end.
func NewExporterFromInternalForTestWithTransportOptions(
	inner *internalapp.Exporter,
	tr listenerFactory,
	opts TransportOptions,
) *Exporter {
	return &Exporter{
		inner:     inner,
		cfg:       exporterConfig{transportOptions: opts},
		transport: tr,
	}
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
	cfg := newImporterConfig()

	for _, opt := range opts {
		opt(&cfg)
	}

	return ImporterConfigForTest{inner: cfg}
}

// LoggerForTest returns the stored logger (or nil if unset).
func (c ImporterConfigForTest) LoggerForTest() *slog.Logger { return c.inner.logger }

// BackoffForTest returns the stored backoff strategy (or nil if unset).
func (c ImporterConfigForTest) BackoffForTest() BackoffStrategy { return c.inner.backoff }

// BackoffFactoryForTest returns the configured per-attachment factory.
func (c ImporterConfigForTest) BackoffFactoryForTest() BackoffFactory {
	if c.inner.backoffFactory == nil {
		return nil
	}

	return c.inner.backoffFactory.newStrategy
}

// StatusPollIntervalForTest returns the stored poll interval.
func (c ImporterConfigForTest) StatusPollIntervalForTest() time.Duration {
	return c.inner.statusPollInterval
}

// TransportOptions returns the stored TransportOptions snapshot.
// Tests use this to prove WithImporterTransportOptions stores its
// argument verbatim onto the public config.
func (c ImporterConfigForTest) TransportOptions() TransportOptions {
	return c.inner.transportOptions
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

// AcceptRateLimitSetForTest reports whether the option was explicitly
// supplied, distinguishing an omitted default from an explicit disabled zero.
func (c ExporterConfigForTest) AcceptRateLimitSetForTest() bool {
	return c.inner.acceptRateLimitSet
}

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

// TransportOptions returns the stored TransportOptions snapshot.
// Tests use this to prove WithExporterTransportOptions stores its
// argument verbatim onto the public config.
func (c ExporterConfigForTest) TransportOptions() TransportOptions {
	return c.inner.transportOptions
}
