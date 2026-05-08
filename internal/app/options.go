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
