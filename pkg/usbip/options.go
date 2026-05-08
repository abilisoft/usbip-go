// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip

import (
	"log/slog"
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/prometheus/client_golang/prometheus"
)

// importerConfig carries the option-tunable fields for NewImporter.
// The struct is unexported so the option shape can evolve without a
// breaking change; callers only manipulate the With* functions declared
// alongside.
type importerConfig struct {
	logger             *slog.Logger
	backoff            BackoffStrategy
	statusPollInterval time.Duration
	metricsRegisterer  prometheus.Registerer
	// transportOptions is the snapshot wired into internal/app via
	// WithImporterTransportOptions during config translation. Zero
	// preserves v1.0.0 behavior; non-zero values reach the dialed
	// connection through the transport adapter.
	transportOptions TransportOptions
}

// ImporterOption configures an Importer at construction time. Apply
// options by passing them to NewImporter; options mutate an internal
// config struct in declaration order so the last option wins for any
// field.
type ImporterOption func(*importerConfig)

// WithImporterLogger installs l as the Importer's structured logger.
// A nil logger is accepted and selects slog.Default() at construction
// time, matching the internal convention.
func WithImporterLogger(l *slog.Logger) ImporterOption {
	return func(c *importerConfig) { c.logger = l }
}

// WithImporterBackoff sets the BackoffStrategy handed to AttachOptions
// when the caller leaves the per-Attach Backoff field nil. A nil
// strategy falls back to the library default (exponential, min 1s, max
// 60s, 20% jitter).
func WithImporterBackoff(b BackoffStrategy) ImporterOption {
	return func(c *importerConfig) { c.backoff = b }
}

// WithImporterStatusPollInterval sets the reconnect watcher's backstop
// poll period. Zero picks up the library default (5 seconds); a
// negative value disables polling entirely. v1 contract §5.5.
func WithImporterStatusPollInterval(d time.Duration) ImporterOption {
	return func(c *importerConfig) { c.statusPollInterval = d }
}

// WithImporterMetricsRegisterer records a Prometheus registerer that
// the Importer uses to publish the §11.5.5 metric catalog. A nil
// registerer (the default) yields a no-op metrics bundle.
func WithImporterMetricsRegisterer(r prometheus.Registerer) ImporterOption {
	return func(c *importerConfig) { c.metricsRegisterer = r }
}

// WithImporterTransportOptions stores TCP-level tuning that the
// Importer hands to the transport adapter on every outbound Dial.
// Zero-valued fields preserve v1.0.0 behavior. Negative values cause
// NewImporter to return ErrTransportOptionsInvalid.
//
// Recommended values per RTT class are documented in
// `docs/high-latency-plan.md`. Common WAN starting point:
//
//	usbip.WithImporterTransportOptions(usbip.TransportOptions{
//	    DialConnectTimeout:   10 * time.Second,
//	    TCPKeepAliveIdle:     30 * time.Second,
//	    TCPKeepAliveInterval: 10 * time.Second,
//	    TCPKeepAliveProbes:   6,
//	})
func WithImporterTransportOptions(opts TransportOptions) ImporterOption {
	return func(c *importerConfig) { c.transportOptions = opts }
}

// importerConfigToInternal translates the public-facing importerConfig
// into the matching slice of internalapp.ImporterOption values. Fields
// that have no internal counterpart yet (backoff, statusPollInterval)
// are carried on the public config but consumed from AttachOptions at
// Attach time; this keeps the public surface stable while the internal
// Importer grows its per-Importer defaults.
func importerConfigToInternal(cfg importerConfig) []internalapp.ImporterOption {
	const importerInternalOptCap = 3

	out := make([]internalapp.ImporterOption, 0, importerInternalOptCap)

	if cfg.logger != nil {
		out = append(out, internalapp.WithImporterLogger(cfg.logger))
	}

	if cfg.metricsRegisterer != nil {
		out = append(out, internalapp.WithImporterMetrics(
			internalapp.MustNewMetrics(cfg.metricsRegisterer)))
	}

	if cfg.transportOptions != (TransportOptions{}) {
		out = append(out, internalapp.WithImporterTransportOptions(cfg.transportOptions))
	}

	return out
}

// exporterConfig carries the option-tunable fields for NewExporter.
// The split from importerConfig prevents a single Option type from
// accepting importer-only or exporter-only tunables at the wrong
// constructor (v1 contract §5.7: role-specific options).
type exporterConfig struct {
	logger             *slog.Logger
	maxSessions        int
	maxSessionsPerPeer int
	acceptRateLimit    float64
	allowCIDRs         []string
	maxHandshakeBytes  int
	handshakeTimeout   time.Duration
	// shutdownTimeout is forwarded into internal/app's
	// WithExporterShutdownTimeout by exporterConfigToInternal.
	shutdownTimeout time.Duration
	// metricsRegisterer is forwarded into internal/app's
	// WithExporterMetrics by exporterConfigToInternal, which registers
	// the §11.5.5 collectors against this registerer.
	metricsRegisterer prometheus.Registerer
	// buildInfo labels the usbip_build_info gauge at construction time.
	// Zero value means "do not stamp"; exporterConfigToInternal skips
	// the option when all three fields are empty.
	buildInfo exporterBuildInfo
	// transportOptions is wired through to internal/app via
	// WithExporterTransportOptions. Today the value reaches accepted
	// connections only through the transport adapter's Listen wrapper;
	// callers that pass a pre-built net.Listener to Serve must tune
	// that listener themselves.
	transportOptions TransportOptions
}

// exporterBuildInfo mirrors the internal/app buildInfo shape so the
// public surface does not leak internal types.
type exporterBuildInfo struct {
	version   string
	commit    string
	goVersion string
}

// ExporterOption configures an Exporter at construction time. Apply
// options by passing them to NewExporter; options mutate an internal
// config struct in declaration order so the last option wins for any
// field.
type ExporterOption func(*exporterConfig)

// WithExporterLogger installs l as the Exporter's structured logger.
// A nil logger is accepted and selects slog.Default() at construction
// time.
func WithExporterLogger(l *slog.Logger) ExporterOption {
	return func(c *exporterConfig) { c.logger = l }
}

// WithExporterMaxSessions caps the total concurrent accepted sessions
// (v1 contract §11.5.3). Zero picks up the library default; a negative value
// disables the cap entirely.
func WithExporterMaxSessions(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxSessions = n }
}

// WithExporterMaxSessionsPerPeer caps the concurrent sessions per
// source IP (v1 contract §11.5.3). Zero picks up the library default; a
// negative value disables the per-peer cap entirely.
func WithExporterMaxSessionsPerPeer(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxSessionsPerPeer = n }
}

// WithExporterAcceptRateLimit caps new accepts at rps tokens per
// second via an internal token bucket with a library-default burst
// size (v1 contract §11.5.3). rps <= 0 disables rate limiting entirely.
func WithExporterAcceptRateLimit(rps float64) ExporterOption {
	return func(c *exporterConfig) { c.acceptRateLimit = rps }
}

// WithExporterAllowCIDR appends CIDR strings to the accept-path allow-
// list (v1 contract §11.5.2). Multiple calls accumulate. An empty list means
// "allow every peer" to match upstream usbip-utils behaviour; at least
// one CIDR opts the exporter into fail-closed ACL enforcement. Invalid
// CIDR strings surface as NewExporter construction errors.
func WithExporterAllowCIDR(cidrs ...string) ExporterOption {
	return func(c *exporterConfig) {
		c.allowCIDRs = append(c.allowCIDRs, cidrs...)
	}
}

// WithExporterMaxHandshakeBytes caps bytes read during the handshake
// phase (v1 contract §11.5.3). Zero picks up the library default.
func WithExporterMaxHandshakeBytes(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxHandshakeBytes = n }
}

// WithExporterHandshakeTimeout bounds how long the exporter waits for
// a client to complete its OP request (v1 contract §11.5.3). Zero picks up
// the library default; a negative value disables the timeout.
func WithExporterHandshakeTimeout(d time.Duration) ExporterOption {
	return func(c *exporterConfig) { c.handshakeTimeout = d }
}

// WithExporterShutdownTimeout applies a backstop deadline to
// Exporter.Shutdown(ctx) when the caller passes a ctx without its own
// deadline. A positive value caps the drain; zero disables the
// backstop; a caller-supplied ctx deadline always wins when tighter.
// v1 contract §5.7.
func WithExporterShutdownTimeout(d time.Duration) ExporterOption {
	return func(c *exporterConfig) { c.shutdownTimeout = d }
}

// WithExporterMetricsRegisterer records a Prometheus registerer that
// the Exporter uses to publish the §11.5.5 metric catalog. A nil
// registerer (the default) yields a no-op metrics bundle: every typed
// accessor inside the app layer is gated on the bundle's nop flag, so
// call sites don't need pre-call nil guards.
//
// Only the first non-nil registerer wins per Exporter; calling this
// option twice replaces the previous value.
func WithExporterMetricsRegisterer(r prometheus.Registerer) ExporterOption {
	return func(c *exporterConfig) { c.metricsRegisterer = r }
}

// WithExporterTransportOptions stores TCP-level tuning that the
// Exporter hands to the transport adapter on accepted listener
// connections. Zero-valued fields preserve v1.0.0 behavior. Negative
// values cause NewExporter to return ErrTransportOptionsInvalid.
//
// IMPORTANT: tuning reaches accepted connections only when the
// listener was produced by the transport adapter's Listen wrapper.
// Daemons that pass a pre-built net.Listener (e.g. systemd
// activation) into Serve must apply socket options to that listener
// themselves; the option is silently inert in that path.
func WithExporterTransportOptions(opts TransportOptions) ExporterOption {
	return func(c *exporterConfig) { c.transportOptions = opts }
}

// WithExporterBuildInfo stamps the usbip_build_info gauge (§11.5.5)
// with the supplied labels at Exporter construction. The labels appear
// in /metrics immediately, before any workload runs. An all-empty
// triple is a no-op so callers that leave the option unspecified do
// not clobber an existing stamp with blanks.
//
// Wiring the stamp through the exporter's own metrics bundle keeps
// metric-collector registration exactly-once per registerer. Callers
// MUST NOT additionally invoke app.MustNewMetrics or SetBuildInfo on
// the same registerer — doing so duplicate-registers the §11.5.5
// catalog and panics at startup.
func WithExporterBuildInfo(version, commit, goVersion string) ExporterOption {
	return func(c *exporterConfig) {
		c.buildInfo = exporterBuildInfo{
			version:   version,
			commit:    commit,
			goVersion: goVersion,
		}
	}
}

// exporterConfigToInternal translates the public-facing exporterConfig
// into the matching slice of internalapp.ExporterOption values. Every
// non-zero field forwards to the internal option space; shutdownTimeout
// is plumbed via internalapp.WithExporterShutdownTimeout.
// metricsRegisterer is plumbed via internalapp.WithExporterMetrics
// (MustNewMetrics(registerer)).
func exporterConfigToInternal(cfg exporterConfig) []internalapp.ExporterOption {
	out := make([]internalapp.ExporterOption, 0, exporterInternalOptCap)

	out = appendExporterLoggerAndLimits(out, cfg)
	out = appendExporterTimeouts(out, cfg)
	out = appendExporterMetrics(out, cfg)

	return out
}

// appendExporterLoggerAndLimits forwards the logger + capacity caps.
// Split from exporterConfigToInternal so each helper stays under the
// project's cyclomatic cap.
func appendExporterLoggerAndLimits(
	out []internalapp.ExporterOption, cfg exporterConfig,
) []internalapp.ExporterOption {
	if cfg.logger != nil {
		out = append(out, internalapp.WithExporterLogger(cfg.logger))
	}

	if cfg.maxSessions != 0 {
		out = append(out, internalapp.WithExporterMaxSessions(cfg.maxSessions))
	}

	if cfg.maxSessionsPerPeer != 0 {
		out = append(out, internalapp.WithExporterMaxSessionsPerPeer(cfg.maxSessionsPerPeer))
	}

	if cfg.acceptRateLimit != 0 {
		// Zero burst picks up the internal default; exposing only rps
		// on the public API matches v1 contract §5.7 verbatim.
		out = append(out, internalapp.WithExporterAcceptRateLimit(cfg.acceptRateLimit, 0))
	}

	if len(cfg.allowCIDRs) > 0 {
		out = append(out, internalapp.WithExporterACL(cfg.allowCIDRs...))
	}

	return out
}

// appendExporterTimeouts forwards the handshake / shutdown timing
// knobs plus the handshake-byte cap.
func appendExporterTimeouts(
	out []internalapp.ExporterOption, cfg exporterConfig,
) []internalapp.ExporterOption {
	if cfg.maxHandshakeBytes != 0 {
		out = append(out, internalapp.WithExporterMaxHandshakeBytes(cfg.maxHandshakeBytes))
	}

	if cfg.handshakeTimeout != 0 {
		out = append(out, internalapp.WithExporterHandshakeTimeout(cfg.handshakeTimeout))
	}

	if cfg.shutdownTimeout != 0 {
		out = append(out, internalapp.WithExporterShutdownTimeout(cfg.shutdownTimeout))
	}

	return out
}

// appendExporterMetrics forwards the Prometheus registerer + build-
// info stamp.
func appendExporterMetrics(
	out []internalapp.ExporterOption, cfg exporterConfig,
) []internalapp.ExporterOption {
	if cfg.metricsRegisterer != nil {
		out = append(out, internalapp.WithExporterMetrics(
			internalapp.MustNewMetrics(cfg.metricsRegisterer)))
	}

	if !cfg.buildInfo.isZero() {
		out = append(out, internalapp.WithExporterBuildInfo(
			cfg.buildInfo.version, cfg.buildInfo.commit, cfg.buildInfo.goVersion))
	}

	if cfg.transportOptions != (TransportOptions{}) {
		out = append(out, internalapp.WithExporterTransportOptions(cfg.transportOptions))
	}

	return out
}

// isZero reports whether bi carries no build-info labels. Drives the
// skip-the-option path in exporterConfigToInternal so an unset
// buildInfo never overwrites an existing stamp with empty labels.
func (bi exporterBuildInfo) isZero() bool {
	return bi.version == "" && bi.commit == "" && bi.goVersion == ""
}

// exporterInternalOptCap is the ceiling used to preallocate the slice
// returned by exporterConfigToInternal. It matches the number of
// option branches inside that function (10 including the shutdown-
// timeout plumbing).
const exporterInternalOptCap = 10
