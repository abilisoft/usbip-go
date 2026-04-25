// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"log/slog"
	"time"
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
	metrics   *Metrics
	// transportOptions is the per-Importer TCP-level tuning bag. The
	// zero value preserves v1.0.0 behavior; PR 1b wires non-zero
	// fields through the adapter.
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

// WithImporterMetrics injects the §11.5.5 metrics bundle. A nil *Metrics
// opts the Importer into the no-op accessor path already implemented by
// MustNewMetrics — call sites don't need a pre-call nil guard.
func WithImporterMetrics(m *Metrics) ImporterOption {
	return func(c *importerConfig) { c.metrics = m }
}

// WithImporterTransportOptions stores TCP-level tuning that the
// Importer's Dial calls hand to the Transport adapter. Zero-valued
// fields preserve v1.0.0 behavior; non-zero fields take effect once
// PR 1b lands the adapter side. NewImporter validates the struct and
// panics on negative values so a misconfigured caller surfaces the
// error at construction time rather than as a confusing socket error
// later.
func WithImporterTransportOptions(opts TransportOptions) ImporterOption {
	return func(c *importerConfig) { c.transportOptions = opts }
}

// ExporterOption configures an Exporter at construction time. Apply
// options by passing them to NewExporter; options mutate an internal
// config struct in declaration order so the last option wins for any
// field. The split from ImporterOption (not a unified Option type) is
// deliberate per v1 contract §9.3: a unified type would let WithMaxSessions
// compile against an Importer, which is a typed programming error.
type ExporterOption func(*exporterConfig)

// exporterConfig is the mutable bag of dependencies and limits that
// option functions populate. Exposed to tests via option setters; never
// returned from a public API. Resource-limit fields follow v1 contract §11.5.3;
// zero means "apply the documented default".
type exporterConfig struct {
	kernel    ExporterKernel
	events    KernelEvents
	transport Transport
	codec     ProtocolCodec
	clock     Clock
	logger    *slog.Logger
	metrics   *Metrics
	// transportOptions is the per-Exporter TCP-level tuning bag. The
	// zero value preserves v1.0.0 behavior; PR 1b wires non-zero
	// fields through the listener-accept path.
	transportOptions TransportOptions

	maxSessions        int
	maxSessionsPerPeer int
	acceptRateLimit    float64
	acceptBurst        int
	maxHandshakeBytes  int
	handshakeTimeout   time.Duration
	// shutdownTimeout is the internal backstop applied by Exporter.Shutdown
	// when the caller's ctx carries no deadline. Zero means "no backstop"
	// — Shutdown respects only the caller's ctx.
	shutdownTimeout time.Duration

	aclCIDRs []string

	buildInfo buildInfo
}

// buildInfo carries version / commit / goVersion labels for the
// usbip_build_info gauge (§11.5.5). Zero-value means "do not stamp";
// NewExporter skips the SetBuildInfo call in that case so a bundle
// wired against a nil registerer stays fully no-op.
type buildInfo struct {
	version   string
	commit    string
	goVersion string
}

// empty reports whether bi carries no build-info labels. An all-zero
// buildInfo is the signal to skip SetBuildInfo at construction.
func (bi buildInfo) empty() bool {
	return bi.version == "" && bi.commit == "" && bi.goVersion == ""
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

// WithExporterTransport injects the TCP transport. Required.
func WithExporterTransport(t Transport) ExporterOption {
	return func(c *exporterConfig) { c.transport = t }
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

// WithExporterMetrics injects the §11.5.5 metrics bundle. A nil *Metrics
// opts the Exporter into the no-op accessor path implemented by
// MustNewMetrics — call sites don't need a pre-call nil guard.
func WithExporterMetrics(m *Metrics) ExporterOption {
	return func(c *exporterConfig) { c.metrics = m }
}

// WithExporterTransportOptions stores TCP-level tuning that the
// Exporter's Listen calls hand to the Transport adapter. Zero-valued
// fields preserve v1.0.0 behavior. NewExporterWithError validates the
// struct and returns ErrTransportOptionsInvalid on negative values
// (matching the ACL-validation precedent); NewExporter panics with
// the same error.
func WithExporterTransportOptions(opts TransportOptions) ExporterOption {
	return func(c *exporterConfig) { c.transportOptions = opts }
}

// WithExporterBuildInfo stamps the usbip_build_info gauge (§11.5.5)
// with the supplied labels at Exporter construction time. The labels
// appear in /metrics immediately, before any workload runs. An all-
// empty triple is a no-op so constructors that leave the option
// unspecified do not clobber an existing stamp with blanks.
//
// This option replaces the previous pattern of calling
// Metrics.SetBuildInfo from the daemon bootstrap path, which forced
// the caller to reach MustNewMetrics a SECOND time against the same
// registry — panicking on duplicate registration. Wiring the stamp
// through the exporter's own bundle keeps registration exactly-once.
func WithExporterBuildInfo(version, commit, goVersion string) ExporterOption {
	return func(c *exporterConfig) {
		c.buildInfo = buildInfo{
			version:   version,
			commit:    commit,
			goVersion: goVersion,
		}
	}
}

// WithExporterMaxSessions caps the total concurrent accepted sessions
// (v1 contract §11.5.3). Zero picks up the default; a negative value disables
// the cap entirely. Each accepted connection that would push the count
// past the cap is closed by the handler before ExportOnConn runs, so
// the kernel is never asked to attach past the cap.
func WithExporterMaxSessions(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxSessions = n }
}

// WithExporterMaxSessionsPerPeer caps the concurrent sessions per
// source IP (v1 contract §11.5.3). Zero picks up the default; a negative
// value disables the per-peer cap entirely.
func WithExporterMaxSessionsPerPeer(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxSessionsPerPeer = n }
}

// WithExporterAcceptRateLimit caps new accepts at rps tokens per
// second via a token bucket with the given burst size (v1 contract §11.5.3).
// rps <= 0 disables rate limiting entirely; burst <= 0 picks up a
// sane default.
func WithExporterAcceptRateLimit(rps float64, burst int) ExporterOption {
	return func(c *exporterConfig) {
		c.acceptRateLimit = rps
		c.acceptBurst = burst
	}
}

// WithExporterMaxHandshakeBytes caps bytes read during the handshake
// phase (v1 contract §11.5.3). Zero picks up the default.
func WithExporterMaxHandshakeBytes(n int) ExporterOption {
	return func(c *exporterConfig) { c.maxHandshakeBytes = n }
}

// WithExporterHandshakeTimeout bounds how long the exporter will wait
// for a client to complete its OP request (v1 contract §11.5.3). Zero picks
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

// WithExporterACL appends CIDR strings to the accept-path allow-list
// (v1 contract §11.5.2). Multiple calls accumulate. An empty list means
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
	// AutoReconnect enables the reconnect watcher. When true, Attach
	// spawns a watcher goroutine that re-establishes the attach after
	// uevent- or poll-detected detach.
	AutoReconnect bool

	// Backoff computes delays between reconnect attempts. When nil
	// and AutoReconnect is true, the watcher defaults to
	// ExponentialBackoff{Min:1s, Max:60s, Jitter:0.2}.
	Backoff BackoffStrategy

	// MaxAttempts caps the number of reconnect retries. Zero means
	// infinite.
	MaxAttempts int

	// OnReconnect is invoked before every retry with the 1-indexed
	// attempt number and the error that triggered the retry. nil
	// disables the callback.
	//
	// The callback is invoked from a separate goroutine so a slow
	// callback cannot stall the retry cadence. It may be called
	// concurrently with other Importer operations (Detach, Close, or
	// an in-flight reconnect). Panics from the callback are recovered
	// and logged via the Importer's logger but are not propagated to
	// the caller or the watcher goroutine.
	OnReconnect func(attempt int, err error)

	// StatusPollInterval controls the backstop poll period. Defaults
	// to 5 seconds when zero; a negative value disables the poll
	// entirely.
	StatusPollInterval time.Duration

	// ShutdownTimeout bounds how long Detach and Close are willing to
	// wait for the watcher goroutine (and any in-flight Detach-driven
	// sysfs write) to drain before proceeding anyway. Zero means use
	// the §5.5 default of 5 seconds; a negative value disables the
	// bound (wait indefinitely).
	ShutdownTimeout time.Duration
}
