// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"net"
	"strings"
	"sync"
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
// error (public-library-api OpenSpec).
//
// The translation is scoped to internal sentinels that would otherwise leak
// across the boundary. Any other error passes
// through unchanged so adapter-level wrap chains (fmt.Errorf with %w
// from transport/kernel/codec) reach the caller intact.
func translateInternalErr(err error) error {
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, internalapp.ErrImporterClosed):
		return ErrImporterClosed
	case errors.Is(err, internalapp.ErrEventStreamClosed):
		return ErrEventStreamClosed
	case errors.Is(err, internalapp.ErrAlreadyShutdown):
		return ErrExporterShutdown
	case errors.Is(err, internalapp.ErrServeAlreadyRunning):
		return ErrServeAlreadyRunning
	case errors.Is(err, internalapp.ErrAttachOptionsInvalid):
		// Replace the internal sentinel with the facade alias while
		// preserving the detail text. Using the message string
		// (not the error value) in the trailing verb is deliberate:
		// wrapping the internal error would re-expose its identity
		// via errors.Is and break the package boundary contract.
		detail := strings.TrimPrefix(err.Error(),
			internalapp.ErrAttachOptionsInvalid.Error()+": ")

		return fmt.Errorf("%w: %s", ErrAttachOptionsInvalid, detail)
	case errors.Is(err, internalapp.ErrACLInvalid):
		detail := strings.TrimPrefix(err.Error(), internalapp.ErrACLInvalid.Error()+": ")

		return fmt.Errorf("%w: %s", ErrACLInvalid, detail)
	case errors.Is(err, internalapp.ErrAcceptRateLimitInvalid):
		detail := strings.TrimPrefix(err.Error(), internalapp.ErrAcceptRateLimitInvalid.Error()+": ")

		return fmt.Errorf("%w: %s", ErrAcceptRateLimitInvalid, detail)
	}

	return err
}

// Pure-data types are aliased to pkg/domain so consumers referencing
// usbip.Device and domain.Device get the same type. Aliasing instead of
// redeclaring means a value from one package drops into a parameter of
// the other without conversion. public-library-api OpenSpec.
type (
	// Device describes a USB device enumerated over USB/IP.
	Device = domain.Device

	// Port describes one vhci kernel status row on the importer side.
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

	// EventKind is the discriminator of the closed Event union.
	EventKind = domain.EventKind

	// PortAttachedEvent is emitted when a remote device attaches.
	PortAttachedEvent = domain.PortAttachedEvent

	// PortDetachedEvent is emitted when a previously-attached port is released.
	PortDetachedEvent = domain.PortDetachedEvent

	// PortErroredEvent is emitted when the vhci port transitions to error.
	PortErroredEvent = domain.PortErroredEvent

	// PortReconnectExhaustedEvent is emitted by the importer's reconnect
	// watcher when MaxAttempts is reached without a successful reattach.
	// Carries a snapshot of the last successful Port plus attempt count
	// and stringified last error. See openspec/specs/domain-model/spec.md.
	PortReconnectExhaustedEvent = domain.PortReconnectExhaustedEvent

	// DeviceBoundEvent is emitted when a local device becomes exportable.
	DeviceBoundEvent = domain.DeviceBoundEvent

	// DeviceUnboundEvent is emitted when a local device is unbound.
	DeviceUnboundEvent = domain.DeviceUnboundEvent

	// SessionStartedEvent is emitted when a client completes the handshake.
	SessionStartedEvent = domain.SessionStartedEvent

	// SessionEndedEvent is emitted when a Session closes for any reason.
	SessionEndedEvent = domain.SessionEndedEvent

	// PortID identifies a vhci port numerically.
	PortID = domain.PortID
)

// EventKind constants re-exported for consumers comparing
// domain.Event values without importing pkg/domain directly.
const (
	EventPortAttached           = domain.EventPortAttached
	EventPortDetached           = domain.EventPortDetached
	EventPortErrored            = domain.EventPortErrored
	EventPortReconnectExhausted = domain.EventPortReconnectExhausted
	EventDeviceBound            = domain.EventDeviceBound
	EventDeviceUnbound          = domain.EventDeviceUnbound
	EventSessionStarted         = domain.EventSessionStarted
	EventSessionEnded           = domain.EventSessionEnded
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

	// OnReconnect receives retry notifications with the 1-indexed attempt
	// number and the error that triggered the retry. nil disables the
	// callback. Notifications run serially outside the retry goroutine; when
	// attempts outpace a slow callback, pending notifications are coalesced
	// to the latest attempt. Panics are recovered and logged via the
	// Importer's logger.
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
// deliberately hidden from the public surface (public-library-api OpenSpec).
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
// defaults (WithImporterBackoff, WithImporterBackoffFactory, and
// WithImporterStatusPollInterval) then
// translated to the internal form before forwarding.
func (i *Importer) Attach(ctx context.Context, r RemoteEndpoint, busID BusID, opts AttachOptions) (Port, error) {
	port, err := i.inner.Attach(ctx, r, busID,
		i.attachOptionsToInternal(i.mergeAttachOptions(opts)))

	return port, translateInternalErr(err)
}

// Detach tears down a kernel-owned vhci port. The Port may have been attached
// by this Importer or inherited from an earlier Importer or process.
func (i *Importer) Detach(ctx context.Context, id PortID) error {
	return translateInternalErr(i.inner.Detach(ctx, id))
}

// ListPorts returns the kernel's vhci status rows, including free-capacity
// rows whose Status is Null or Available and claimed NotAssigned rows that are
// still waiting for their local USB address.
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

// WatchWithErrors returns an error-aware event iterator. Ordinary events are
// paired with nil; subscription failures preserve their wrapped cause; and an
// unexpectedly closed established source yields ErrEventStreamClosed. Caller
// cancellation and Importer.Close end the iterator without an error.
func (i *Importer) WatchWithErrors(ctx context.Context) iter.Seq2[Event, error] {
	return func(yield func(Event, error) bool) {
		for event, watchErr := range i.inner.WatchWithErrors(ctx) {
			if !yield(event, translateInternalErr(watchErr)) {
				return
			}
		}
	}
}

// Close cancels every active port handle, drains background goroutines,
// and marks the Importer closed. Idempotent via the internal sync.Once.
func (i *Importer) Close() error {
	return translateInternalErr(i.inner.Close())
}

// attachOptionsToInternal translates the public AttachOptions shape and the
// Importer's configured defaults to internal app options. Custom strategy
// calls share one facade-owned mutex for v1 compatibility; a configured
// factory still supplies independent state per logical attachment.
func (i *Importer) attachOptionsToInternal(a AttachOptions) internalapp.AttachOptions {
	out := internalapp.AttachOptions{
		AutoReconnect:      a.AutoReconnect,
		MaxAttempts:        a.MaxAttempts,
		OnReconnect:        a.OnReconnect,
		StatusPollInterval: a.StatusPollInterval,
		ShutdownTimeout:    a.ShutdownTimeout,
	}

	switch {
	case a.Backoff != nil:
		out.Backoff = backoffToInternal(a.Backoff, i.cfg.backoffMu)
	case i.cfg.backoffFactory != nil:
		factory := i.cfg.backoffFactory.newStrategy

		out.BackoffFactory = func() internalapp.BackoffStrategy {
			// A factory guarantees a separately owned strategy, so its adapter
			// receives a per-Attachment lock rather than the compatibility lock
			// shared by legacy strategy values.
			return backoffToInternal(factory(), &sync.Mutex{})
		}
	case i.cfg.backoff != nil:
		out.Backoff = backoffToInternal(i.cfg.backoff, i.cfg.backoffMu)
	}

	return out
}

// mergeAttachOptions overlays importer-level defaults onto the per-
// call AttachOptions. Caller-supplied fields win; unset fields pick
// up the corresponding WithImporter* value (if any).
func (i *Importer) mergeAttachOptions(opts AttachOptions) AttachOptions {
	if opts.StatusPollInterval == 0 {
		opts.StatusPollInterval = i.cfg.statusPollInterval
	}

	return opts
}

// Exporter is the public wrapper around internalapp.Exporter. Method
// bodies forward after argument translation. Construct via NewExporter;
// the zero value is not usable.
//
// transport is the same TCP adapter the inner Exporter was built
// with. Storing it on the public wrapper lets ListenAndServe call
// Transport.Listen with the stored TransportOptions so accepted
// connections inherit the requested tuning. Serve(ctx, listener)
// retains its caller-supplied-listener semantics; ListenAndServe is
// the option-honoring counterpart for callers that do not need
// systemd-activation or other foreign listener wiring.
type Exporter struct {
	inner     *internalapp.Exporter
	cfg       exporterConfig
	transport listenerFactory
}

type listenerFactory interface {
	Listen(ctx context.Context, addr string, opts internalapp.TransportOptions) (net.Listener, error)
}

// NewExporter constructs an Exporter backed by the default Linux
// kernel, uevent, transport, and codec adapters. Options apply in
// declaration order. Non-Linux callers receive ErrKernelModuleMissing.
func NewExporter(opts ...ExporterOption) (*Exporter, error) {
	return newDefaultExporter(opts)
}

// ListAvailable enumerates every USB device on the host regardless
// of bind state — the CLI's `usbip-go list` view.
func (e *Exporter) ListAvailable(ctx context.Context) ([]Device, error) {
	devs, err := e.inner.ListAvailable(ctx)

	return devs, translateInternalErr(err)
}

// ListExported returns the currently-bound subset: devices whose
// driver is usbip-host AND that are not actively claimed by an
// importer (SDEV_ST_USED excluded). This is what peers see via the
// OP_REP_DEVLIST wire reply and what the status socket reports as
// "bound_devices". Distinct from ListAvailable which dumps the full
// local USB bus.
func (e *Exporter) ListExported(ctx context.Context) ([]Device, error) {
	devs, err := e.inner.ListExported(ctx)

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

// ListenAndServe reserves the Exporter's lifecycle, then binds addr through
// the transport adapter (so accepted connections inherit the
// WithExporterTransportOptions tuning) and runs the accept loop. It is the option-honoring
// counterpart to Serve(ctx, listener) for callers that do not need
// systemd activation or other foreign listener wiring.
//
// Lifecycle rejection precedes Listen. A Listen failure short-circuits the
// reserved call and is surfaced wrapped. Any bound listener is closed
// deterministically before ListenAndServe returns.
func (e *Exporter) ListenAndServe(ctx context.Context, addr string) error {
	var listener net.Listener

	defer func() {
		if listener != nil {
			_ = listener.Close()
		}
	}()

	err := e.inner.ServeWithListenerFactory(
		ctx,
		func(listenCtx context.Context) (net.Listener, error) {
			var listenErr error

			listener, listenErr = e.transport.Listen(listenCtx, addr, e.cfg.transportOptions)
			if listenErr != nil {
				return nil, fmt.Errorf("usbip.Exporter.ListenAndServe: %w", listenErr)
			}

			return listener, nil
		},
	)

	return translateInternalErr(err)
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
