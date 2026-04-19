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
// negative value disables polling entirely. Spec §5.5.
func WithImporterStatusPollInterval(d time.Duration) ImporterOption {
	return func(c *importerConfig) { c.statusPollInterval = d }
}

// importerConfigToInternal translates the public-facing importerConfig
// into the matching slice of internalapp.ImporterOption values. Fields
// that have no internal counterpart yet (backoff, statusPollInterval)
// are carried on the public config but consumed from AttachOptions at
// Attach time; this keeps the public surface stable while the internal
// Importer grows its per-Importer defaults.
func importerConfigToInternal(cfg importerConfig) []internalapp.ImporterOption {
	out := make([]internalapp.ImporterOption, 0, 1)

	if cfg.logger != nil {
		out = append(out, internalapp.WithImporterLogger(cfg.logger))
	}

	return out
}

// exporterConfig carries the option-tunable fields for NewExporter.
// The split from importerConfig prevents a single Option type from
// accepting importer-only or exporter-only tunables at the wrong
// constructor (spec §5.7: role-specific options).
type exporterConfig struct {
	logger             *slog.Logger
	maxSessions        int
	maxSessionsPerPeer int
	acceptRateLimit    float64
	allowCIDRs         []string
	maxHandshakeBytes  int
	handshakeTimeout   time.Duration
	// shutdownTimeout is stored on the public config and consumed by
	// the exporter metrics/lifecycle wiring in a later phase. Keeping
	// the field here now means the public option lands stable from
	// day one — the implementation follow-up plugs it in without a
	// breaking-change bump.
	shutdownTimeout time.Duration
	// metricsRegisterer follows the same "public-first, wire-later"
	// rule as shutdownTimeout. The registerer is stored here and used
	// by the exporter metrics wiring in Phase 9.
	metricsRegisterer prometheus.Registerer
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
// (spec §11.5.3). Zero picks up the library default; a negative value
// disables the cap entirely.
func WithExporterMaxSessions(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxSessions = n }
}

// WithExporterMaxSessionsPerPeer caps the concurrent sessions per
// source IP (spec §11.5.3). Zero picks up the library default; a
// negative value disables the per-peer cap entirely.
func WithExporterMaxSessionsPerPeer(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxSessionsPerPeer = n }
}

// WithExporterAcceptRateLimit caps new accepts at rps tokens per
// second via an internal token bucket with a library-default burst
// size (spec §11.5.3). rps <= 0 disables rate limiting entirely.
func WithExporterAcceptRateLimit(rps float64) ExporterOption {
	return func(c *exporterConfig) { c.acceptRateLimit = rps }
}

// WithExporterAllowCIDR appends CIDR strings to the accept-path allow-
// list (spec §11.5.2). Multiple calls accumulate. An empty list means
// "allow every peer" to match upstream usbip-utils behaviour; at least
// one CIDR opts the exporter into fail-closed ACL enforcement. Invalid
// CIDR strings surface as NewExporter construction errors.
func WithExporterAllowCIDR(cidrs ...string) ExporterOption {
	return func(c *exporterConfig) {
		c.allowCIDRs = append(c.allowCIDRs, cidrs...)
	}
}

// WithExporterMaxHandshakeBytes caps bytes read during the handshake
// phase (spec §11.5.3). Zero picks up the library default.
func WithExporterMaxHandshakeBytes(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxHandshakeBytes = n }
}

// WithExporterHandshakeTimeout bounds how long the exporter waits for
// a client to complete its OP request (spec §11.5.3). Zero picks up
// the library default; a negative value disables the timeout.
func WithExporterHandshakeTimeout(d time.Duration) ExporterOption {
	return func(c *exporterConfig) { c.handshakeTimeout = d }
}

// WithExporterShutdownTimeout bounds the Exporter.Shutdown drain per
// spec §5.7. The value is stored on the public config; the exporter
// metrics/lifecycle wiring consumes it in Phase 9 without a public
// API change.
func WithExporterShutdownTimeout(d time.Duration) ExporterOption {
	return func(c *exporterConfig) { c.shutdownTimeout = d }
}

// WithExporterMetricsRegisterer registers Exporter-side Prometheus
// collectors into r. The registerer is stored on the public config
// and consumed by the metrics wiring in Phase 9; declaring the option
// now keeps the v1 public API stable across the follow-up.
func WithExporterMetricsRegisterer(r prometheus.Registerer) ExporterOption {
	return func(c *exporterConfig) { c.metricsRegisterer = r }
}

// exporterConfigToInternal translates the public-facing exporterConfig
// into the matching slice of internalapp.ExporterOption values. Fields
// without an internal counterpart yet (shutdownTimeout, metricsRegisterer)
// are carried on the public config and wired through in a later phase.
func exporterConfigToInternal(cfg exporterConfig) []internalapp.ExporterOption {
	out := make([]internalapp.ExporterOption, 0, exporterInternalOptCap)

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
		// on the public API matches spec §5.7 verbatim.
		out = append(out, internalapp.WithExporterAcceptRateLimit(cfg.acceptRateLimit, 0))
	}

	if len(cfg.allowCIDRs) > 0 {
		out = append(out, internalapp.WithExporterACL(cfg.allowCIDRs...))
	}

	if cfg.maxHandshakeBytes != 0 {
		out = append(out, internalapp.WithExporterMaxHandshakeBytes(cfg.maxHandshakeBytes))
	}

	if cfg.handshakeTimeout != 0 {
		out = append(out, internalapp.WithExporterHandshakeTimeout(cfg.handshakeTimeout))
	}

	return out
}

// exporterInternalOptCap is the ceiling used to preallocate the slice
// returned by exporterConfigToInternal. It matches the number of
// option branches inside that function.
const exporterInternalOptCap = 7
