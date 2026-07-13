// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// ImporterOption configures an Importer at construction time. Apply
// options by passing them to NewImporter; options mutate an internal
// config struct in declaration order so the last option wins for any
// field.
type ImporterOption func(*importerConfig)

// importerConfig is the mutable bag of dependencies that option
// functions populate. Exposed to tests via option setters; never
// returned from a public API.
type importerConfig struct {
	kernel    ImporterKernel
	events    KernelEvents
	transport Transport
	codec     ProtocolCodec
	clock     Clock
	logger    *slog.Logger
	// transportOptions is the per-Importer TCP-level tuning bag. The
	// zero value preserves v1.0.0 behavior; non-zero fields flow through
	// each Dial call to the transport adapter.
	transportOptions TransportOptions
}

// WithImporterKernel injects the kernel-side adapter (vhci_hcd
// surface). Required — NewImporter fails fast if left nil.
func WithImporterKernel(k ImporterKernel) ImporterOption {
	return func(c *importerConfig) { c.kernel = k }
}

// WithImporterEvents injects the shared uevent source. Required.
func WithImporterEvents(e KernelEvents) ImporterOption {
	return func(c *importerConfig) { c.events = e }
}

// WithImporterTransport injects the TCP transport. Required.
func WithImporterTransport(t Transport) ImporterOption {
	return func(c *importerConfig) { c.transport = t }
}

// WithImporterCodec injects the USBIP wire codec. Required.
func WithImporterCodec(p ProtocolCodec) ImporterOption {
	return func(c *importerConfig) { c.codec = p }
}

// WithImporterClock injects the Clock used for backoff / timers.
// Defaults to RealClock when unspecified.
func WithImporterClock(clk Clock) ImporterOption {
	return func(c *importerConfig) { c.clock = clk }
}

// WithImporterLogger injects the structured logger. Defaults to
// slog.Default() when unspecified.
func WithImporterLogger(l *slog.Logger) ImporterOption {
	return func(c *importerConfig) { c.logger = l }
}

// WithImporterTransportOptions stores TCP-level tuning that the
// Importer's Dial calls hand to the Transport adapter. Zero-valued
// fields preserve v1.0.0 behavior; non-zero fields are applied by the
// adapter. NewImporter validates the struct and panics on negative values
// so a misconfigured caller surfaces the error at construction time rather
// than as a confusing socket error later.
func WithImporterTransportOptions(opts TransportOptions) ImporterOption {
	return func(c *importerConfig) { c.transportOptions = opts }
}

// ExporterOption configures an Exporter at construction time. Apply
// options by passing them to NewExporter; options mutate an internal
// config struct in declaration order so the last option wins for any
// field. The split from ImporterOption (not a unified Option type) is
// deliberate per public-library-api OpenSpec: a unified type would let WithMaxSessions
// compile against an Importer, which is a typed programming error.
type ExporterOption func(*exporterConfig)

// exporterConfig is the mutable bag of dependencies and limits that
// option functions populate. Exposed to tests via option setters; never
// returned from a public API. Resource-limit fields follow security-release-quality OpenSpec;
// zero means "apply the documented default".
type exporterConfig struct {
	kernel       ExporterKernel
	events       KernelEvents
	codec        ProtocolCodec
	clock        Clock
	logger       *slog.Logger
	newSessionID func() (domain.SessionID, error)

	maxSessions        int
	maxSessionsPerPeer int
	acceptRateLimit    float64
	acceptRateLimitSet bool
	acceptBurst        int
	maxHandshakeBytes  int
	handshakeTimeout   time.Duration
	// shutdownTimeout is the internal backstop applied by Exporter.Shutdown
	// when the caller's ctx carries no deadline. Zero means "no backstop"
	// — Shutdown respects only the caller's ctx.
	shutdownTimeout time.Duration
	// statusPollInterval is the internal exporter-session activity
	// backstop cadence. Zero selects the semantic default; a negative
	// value disables polling for focused event-path tests.
	statusPollInterval time.Duration

	aclCIDRs []string
}

// WithExporterKernel injects the kernel-side adapter (usbip_host
// surface). Required — NewExporter fails fast if left nil.
func WithExporterKernel(k ExporterKernel) ExporterOption {
	return func(c *exporterConfig) { c.kernel = k }
}

// WithExporterEvents injects the shared uevent source. Required.
func WithExporterEvents(e KernelEvents) ExporterOption {
	return func(c *exporterConfig) { c.events = e }
}

// WithExporterCodec injects the USBIP wire codec. Required.
func WithExporterCodec(p ProtocolCodec) ExporterOption {
	return func(c *exporterConfig) { c.codec = p }
}

// WithExporterClock injects the Clock used for handshake timeouts and
// drain deadlines. Defaults to RealClock when unspecified.
func WithExporterClock(clk Clock) ExporterOption {
	return func(c *exporterConfig) { c.clock = clk }
}

// WithExporterLogger injects the structured logger. Defaults to
// slog.Default() when unspecified.
func WithExporterLogger(l *slog.Logger) ExporterOption {
	return func(c *exporterConfig) { c.logger = l }
}

// WithExporterMaxSessions caps the total concurrent accepted sessions
// (security-release-quality OpenSpec). Zero picks up the default; a negative value disables
// the cap entirely. Each accepted connection that would push the count
// past the cap is closed by the handler before ExportOnConn runs, so
// the kernel is never asked to attach past the cap.
func WithExporterMaxSessions(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxSessions = n }
}

// WithExporterMaxSessionsPerPeer caps the concurrent sessions per
// source IP (security-release-quality OpenSpec). Zero picks up the default; a negative
// value disables the per-peer cap entirely.
func WithExporterMaxSessionsPerPeer(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxSessionsPerPeer = n }
}

// WithExporterAcceptRateLimit caps new accepts at rps tokens per
// second via a token bucket with the given burst size (security-release-quality OpenSpec).
// Finite rps <= 0 disables rate limiting entirely; burst <= 0 picks up a
// sane default. Non-finite rps is rejected by NewExporterWithError.
func WithExporterAcceptRateLimit(rps float64, burst int) ExporterOption {
	return func(c *exporterConfig) {
		c.acceptRateLimit = rps
		c.acceptRateLimitSet = true
		c.acceptBurst = burst
	}
}

// WithExporterMaxHandshakeBytes caps bytes read during the handshake
// phase (security-release-quality OpenSpec). Zero picks up the default.
func WithExporterMaxHandshakeBytes(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxHandshakeBytes = n }
}

// WithExporterHandshakeTimeout bounds how long the exporter will wait
// for a client to complete its OP request (security-release-quality OpenSpec). Zero picks
// up the default; a negative value disables the timeout.
func WithExporterHandshakeTimeout(d time.Duration) ExporterOption {
	return func(c *exporterConfig) { c.handshakeTimeout = d }
}

// WithExporterShutdownTimeout is the backstop deadline applied by
// Exporter.Shutdown when the caller's ctx carries no deadline. A zero
// value disables the backstop; a positive value caps the drain
// regardless of the caller's ctx. Always overridden by a tighter
// caller-supplied deadline.
func WithExporterShutdownTimeout(d time.Duration) ExporterOption {
	return func(c *exporterConfig) { c.shutdownTimeout = d }
}

// WithExporterStatusPollInterval overrides the internal usbip_status
// backstop cadence. It is an internal-app test/integration seam, not a
// public pkg/usbip option. Zero selects the default; negative disables.
func WithExporterStatusPollInterval(d time.Duration) ExporterOption {
	return func(c *exporterConfig) { c.statusPollInterval = d }
}

// WithExporterACL appends CIDR strings to the accept-path allow-list
// (security-release-quality OpenSpec). Multiple calls accumulate. An empty list means
// "allow every peer" to match upstream usbip-utils behaviour; at
// least one CIDR opts the exporter into fail-closed ACL enforcement.
// Invalid CIDR strings surface as NewExporterWithError constructor
// errors (ErrACLInvalid) rather than deferred Serve-time failures.
func WithExporterACL(cidrs ...string) ExporterOption {
	return func(c *exporterConfig) {
		c.aclCIDRs = append(c.aclCIDRs, cidrs...)
	}
}

// AttachOptions configures a single Importer.Attach call. All fields
// are optional; zero values produce the documented defaults.
type AttachOptions struct {
	// resetBackoffOnSuccess marks an internal reconnect-path Attach. Its
	// successful replacement must reset the shared strategy before the new
	// watcher can call Next. Public callers cannot set this field.
	resetBackoffOnSuccess bool

	// AutoReconnect enables the reconnect watcher. When true, Attach
	// spawns a watcher goroutine that re-establishes the attach after
	// uevent- or poll-detected detach.
	AutoReconnect bool

	// Backoff computes delays between reconnect attempts. When nil
	// and AutoReconnect is true, the watcher defaults to
	// ExponentialBackoff{Min:1s, Max:60s, Jitter:0.2}.
	Backoff BackoffStrategy

	// BackoffFactory is the facade-owned construction seam for custom
	// importer defaults. Attach invokes it at most once for a top-level logical
	// reconnect lineage, then threads the returned strategy through every
	// replacement generation. Callers outside the parent module cannot import
	// internal/app, so this does not expand the public v1 API.
	BackoffFactory func() BackoffStrategy

	// MaxAttempts caps the number of reconnect retries. Zero means
	// infinite.
	MaxAttempts int

	// OnReconnect receives retry notifications with the 1-indexed
	// attempt number and the error that triggered the retry. nil disables
	// the callback.
	//
	// A single separate goroutine invokes callbacks, so they never overlap
	// and a slow callback cannot stall the retry cadence. If attempts
	// outpace the callback, pending notifications are coalesced to the
	// latest attempt. The callback may run concurrently with other Importer
	// operations (Detach, Close, or an in-flight reconnect). Panics are
	// recovered and logged via the Importer's logger but are not propagated
	// to the caller or watcher goroutine.
	OnReconnect func(attempt int, err error)

	// StatusPollInterval controls the backstop poll period. Defaults
	// to 5 seconds when zero; a negative value disables the poll
	// entirely.
	StatusPollInterval time.Duration

	// ShutdownTimeout bounds how long Detach and Close are willing to
	// wait for the watcher goroutine (and any in-flight Detach-driven
	// sysfs write) to drain before proceeding anyway. Zero means use
	// the importer-lifecycle OpenSpec default of 5 seconds; a negative value disables the
	// bound (wait indefinitely).
	ShutdownTimeout time.Duration
}
