package usbip

import (
	"context"
	"errors"
	"iter"
	"net"
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// translateInternalErr maps internal/app lifecycle sentinels onto the
// matching public sentinels declared in errors.go. The returned error
// carries the public identity only: the internal sentinel is replaced,
// not wrapped, so errors.Is(err, internalapp.ErrX) returns false
// downstream of the facade. That enforces the package boundary —
// consumers never need to import internal/app to classify a returned
// error (Spec §5.7).
//
// The translation is scoped to the three internal sentinels that
// would otherwise leak across the boundary. Any other error passes
// through unchanged so adapter-level wrap chains (fmt.Errorf with %w
// from transport/kernel/codec) reach the caller intact.
func translateInternalErr(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, internalapp.ErrImporterClosed):
		return ErrImporterClosed
	case errors.Is(err, internalapp.ErrAlreadyShutdown):
		return ErrExporterShutdown
	case errors.Is(err, internalapp.ErrServeAlreadyRunning):
		return ErrServeAlreadyRunning
	}

	return err
}

// Pure-data types are aliased to pkg/domain so consumers referencing
// usbip.Device and domain.Device get the same type. Aliasing instead of
// redeclaring means a value from one package drops into a parameter of
// the other without conversion. Spec §5.7.
type (
	// Device describes a USB device enumerated over USB/IP.
	Device = domain.Device

	// Port describes an attached vhci port on the importer side.
	Port = domain.Port

	// Session describes a single client connection from the daemon's view.
	Session = domain.Session

	// BusID is the stable USB topology identifier (e.g. "1-1.2").
	BusID = domain.BusID

	// Speed is the negotiated USB speed.
	Speed = domain.Speed

	// Status is a vhci port state (available/used/error/...).
	Status = domain.Status

	// RemoteEndpoint identifies a USB/IP peer by host and port.
	RemoteEndpoint = domain.RemoteEndpoint

	// Event is the closed polymorphic union of domain events emitted
	// on Watch / WatchSessions iterators.
	Event = domain.Event

	// PortID identifies a vhci port numerically.
	PortID = domain.PortID
)

// AttachOptions configures a single Importer.Attach call. All fields
// are optional; zero values produce the documented defaults.
type AttachOptions struct {
	// AutoReconnect enables the reconnect watcher. When true, Attach
	// spawns a watcher goroutine that re-establishes the attach after
	// uevent- or poll-detected detach.
	AutoReconnect bool

	// Backoff computes delays between reconnect attempts. nil selects
	// the library default of ExponentialBackoff{Min:1s, Max:60s, Jitter:0.2}.
	Backoff BackoffStrategy

	// MaxAttempts caps the number of reconnect retries. Zero means
	// infinite.
	MaxAttempts int

	// OnReconnect is invoked before every retry with the 1-indexed
	// attempt number and the error that triggered the retry. nil
	// disables the callback. Panics from the callback are recovered
	// and logged via the Importer's logger.
	OnReconnect func(attempt int, err error)

	// StatusPollInterval controls the backstop poll period. Zero picks
	// up the library default (5 seconds); a negative value disables
	// polling entirely.
	StatusPollInterval time.Duration

	// ShutdownTimeout bounds how long Detach and Close block on a
	// reconnect watcher's wind-down before proceeding with the
	// kernel-side detach. Zero picks up the library default (5
	// seconds); a negative value disables the bound and waits
	// indefinitely for the watcher to exit.
	ShutdownTimeout time.Duration
}

// toInternal translates the public AttachOptions shape to the internal
// app.AttachOptions. Centralising the translation keeps the public
// shape decoupled from internal evolutions.
func (a AttachOptions) toInternal() internalapp.AttachOptions {
	return internalapp.AttachOptions{
		AutoReconnect:      a.AutoReconnect,
		Backoff:            backoffToInternal(a.Backoff),
		MaxAttempts:        a.MaxAttempts,
		OnReconnect:        a.OnReconnect,
		StatusPollInterval: a.StatusPollInterval,
		ShutdownTimeout:    a.ShutdownTimeout,
	}
}

// Importer is the public wrapper around internalapp.Importer. Method
// bodies forward after argument translation so the internal shape can
// evolve without breaking consumers. Construct via NewImporter; the
// zero value is not usable.
type Importer struct {
	inner *internalapp.Importer
	cfg   importerConfig
}

// NewImporter constructs an Importer backed by the default Linux
// kernel, uevent, transport, and codec adapters. Options apply in
// declaration order; the last option wins for any field. Non-Linux
// callers receive ErrKernelModuleMissing — adapter injection is
// deliberately hidden from the public surface (spec §5.7).
func NewImporter(opts ...ImporterOption) (*Importer, error) {
	return newDefaultImporter(opts)
}

// ListRemote dials endpoint and returns its device list.
func (i *Importer) ListRemote(ctx context.Context, r RemoteEndpoint) ([]Device, error) {
	devs, err := i.inner.ListRemote(ctx, r)

	return devs, translateInternalErr(err)
}

// Attach runs the USB/IP import handshake for busID at r and returns
// the attached Port. AttachOptions is merged with the Importer-level
// defaults (WithImporterBackoff, WithImporterStatusPollInterval) then
// translated to the internal form before forwarding.
func (i *Importer) Attach(ctx context.Context, r RemoteEndpoint, busID BusID, opts AttachOptions) (Port, error) {
	port, err := i.inner.Attach(ctx, r, busID, i.mergeAttachOptions(opts).toInternal())

	return port, translateInternalErr(err)
}

// Detach tears down a previously-attached port.
func (i *Importer) Detach(ctx context.Context, id PortID) error {
	return translateInternalErr(i.inner.Detach(ctx, id))
}

// ListPorts returns the kernel's view of currently-attached ports.
func (i *Importer) ListPorts(ctx context.Context) ([]Port, error) {
	ports, err := i.inner.ListPorts(ctx)

	return ports, translateInternalErr(err)
}

// Watch returns an iter.Seq yielding domain events while ctx is live.
// Iteration terminates when the upstream source closes, ctx is
// cancelled, or yield returns false.
func (i *Importer) Watch(ctx context.Context) iter.Seq[Event] {
	return i.inner.Watch(ctx)
}

// Close cancels every active port handle, drains background goroutines,
// and marks the Importer closed. Idempotent via the internal sync.Once.
func (i *Importer) Close() error {
	return translateInternalErr(i.inner.Close())
}

// mergeAttachOptions overlays importer-level defaults onto the per-
// call AttachOptions. Caller-supplied fields win; unset fields pick
// up the corresponding WithImporter* value (if any).
func (i *Importer) mergeAttachOptions(opts AttachOptions) AttachOptions {
	if opts.Backoff == nil {
		opts.Backoff = i.cfg.backoff
	}

	if opts.StatusPollInterval == 0 {
		opts.StatusPollInterval = i.cfg.statusPollInterval
	}

	return opts
}

// Exporter is the public wrapper around internalapp.Exporter. Method
// bodies forward after argument translation. Construct via NewExporter;
// the zero value is not usable.
type Exporter struct {
	inner *internalapp.Exporter
	cfg   exporterConfig
}

// NewExporter constructs an Exporter backed by the default Linux
// kernel, uevent, transport, and codec adapters. Options apply in
// declaration order. Non-Linux callers receive ErrKernelModuleMissing.
func NewExporter(opts ...ExporterOption) (*Exporter, error) {
	return newDefaultExporter(opts)
}

// ListAvailable enumerates locally-exportable devices.
func (e *Exporter) ListAvailable(ctx context.Context) ([]Device, error) {
	devs, err := e.inner.ListAvailable(ctx)

	return devs, translateInternalErr(err)
}

// Bind makes a local device exportable (binds usbip-host).
func (e *Exporter) Bind(ctx context.Context, busID BusID) error {
	return translateInternalErr(e.inner.Bind(ctx, busID))
}

// Unbind returns a previously-bound device to its original driver.
func (e *Exporter) Unbind(ctx context.Context, busID BusID) error {
	return translateInternalErr(e.inner.Unbind(ctx, busID))
}

// Serve runs the accept loop until ctx is cancelled or the listener
// returns a permanent error.
func (e *Exporter) Serve(ctx context.Context, listener net.Listener) error {
	return translateInternalErr(e.inner.Serve(ctx, listener))
}

// Sessions returns a snapshot of currently-accepted sessions, sorted
// by start time.
func (e *Exporter) Sessions(ctx context.Context) []Session {
	return e.inner.Sessions(ctx)
}

// WatchSessions returns an iter.Seq yielding SessionStartedEvent and
// SessionEndedEvent values while ctx is live.
func (e *Exporter) WatchSessions(ctx context.Context) iter.Seq[Event] {
	return e.inner.WatchSessions(ctx)
}

// Shutdown stops accepting new connections and drains in-flight
// sessions bounded by the provided ctx deadline.
func (e *Exporter) Shutdown(ctx context.Context) error {
	return translateInternalErr(e.inner.Shutdown(ctx))
}
