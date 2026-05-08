// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// defaultShutdownTimeout bounds Detach and Close's wait on the reconnect
// watcher per v1 contract §5.5 when AttachOptions.ShutdownTimeout is zero. A
// negative ShutdownTimeout disables the bound (wait indefinitely).
const defaultShutdownTimeout = 5 * time.Second

// Importer is the use-case service that imports remote USB devices via
// the vhci_hcd kernel surface. One Importer is sufficient for a whole
// process; construct via NewImporter and release via Close. The zero
// value is not usable — NewImporter initialises required state.
//
// The handle map tracks every successfully-attached port along with a
// per-handle cancel signal and a monotonically increasing generation.
// The reconnect watcher (v1 contract §5.5) reads the generation to filter
// stale kernel events whose port id was replaced by a successful
// reattach.
type Importer struct {
	kernel    ImporterKernel
	events    KernelEvents
	transport Transport
	codec     ProtocolCodec
	clock     Clock
	logger    *slog.Logger
	// transportOptions is the snapshot taken at NewImporter time. Every
	// Dial inside the importer passes this struct by value so the
	// adapter can apply per-connection tuning; zero preserves v1.0.0
	// behavior.
	transportOptions TransportOptions

	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
	handles   map[domain.PortID]*portHandle
	// inFlight dedupes concurrent Attach calls for the same
	// (remote, busid) pair. Without this guard two callers would race
	// the dial + handshake + AttachRemote sequence and import the same
	// device onto two local ports. Guarded by mu.
	inFlight map[attachKey]struct{}
	nextGen  uint64
	wg       sync.WaitGroup
	// subscribers is the per-call fanout list for app-synthesized port
	// lifecycle events (PortReconnectExhausted). Watch merges these with
	// the upstream KernelEvents subscription so consumers see all port
	// lifecycle events on one stream. Guarded by mu.
	subscribers []*importerEventSubscriber
}

// attachKey is the deduplication key used by Importer.inFlight. Remote
// normalisation is applied by Attach before the lookup so a caller
// passing ":3240" vs "peer:3240" resolves to the same key once the
// endpoint is normalised.
type attachKey struct {
	remote domain.RemoteEndpoint
	busID  domain.BusID
}

// portHandle is the per-port bookkeeping entry for an active import.
// The done channel is closed exactly once (guarded by cancelOnce) when
// Detach or Close fires; the reconnect watcher selects on it to observe
// termination.
//
// Using a channel + sync.Once instead of a context sidesteps the
// containedctx linter while preserving the same semantics: done is a
// broadcast signal, a watcher derives its own ctx at launch time and
// selects on ctx.Done() alongside done.
//
// generation is assigned at registerHandle time from Importer.nextGen
// and stays constant for the lifetime of the handle; it lets the
// reconnect watcher reject stale uevents whose port id was already
// replaced by a successful reattach (v1 contract §5.5).
//
// watcherDone is closed by the reconnect watcher goroutine when it
// exits. Non-AutoReconnect handles leave it nil; Detach and Close read
// it to synchronise with the watcher before issuing the kernel detach.
//
// shutdownTimeout bounds how long Detach and Close are willing to block
// on watcherDone before proceeding anyway. Carried on the handle (not
// on the Importer) because it is set per-Attach and must outlast the
// Attach call itself.
//
// detaching is set by Detach BEFORE cancel so a reconnect watcher still
// parked inside kernel.AttachRemote past the bounded wait cannot
// silently register a fresh handle for the device the user asked to
// release. The watcher checks this flag AFTER Attach returns success
// and rolls back the kernel handoff when it finds the flag set. Using
// atomic.Bool sidesteps the "watcher holds mu" deadlock risk: the
// watcher reads via Load without touching the Importer mutex.
type portHandle struct {
	done            chan struct{}
	cancelOnce      sync.Once
	busID           domain.BusID
	remote          domain.RemoteEndpoint
	generation      uint64
	watcherDone     chan struct{}
	shutdownTimeout time.Duration
	detaching       atomic.Bool
	// lastKnownPort is the Port snapshot taken at the most recent
	// successful Attach (initial or reconnect). The reconnect watcher
	// emits this value inside PortReconnectExhaustedEvent when
	// MaxAttempts is reached: the kernel slot is gone by that point,
	// so this is the truthful "last viable" view. Guarded by
	// Importer.mu (writes happen under the importer lock).
	lastKnownPort domain.Port
}

// cancel closes the done channel exactly once, signalling any watcher
// to exit. Safe to call repeatedly from different goroutines.
func (h *portHandle) cancel() {
	h.cancelOnce.Do(func() { close(h.done) })
}

// NewImporter constructs an Importer from functional options. The
// returned *Importer is safe for concurrent use. Required dependencies
// (kernel, events, transport, codec) must be supplied via their
// With... option funcs; NewImporter panics if any are nil because a
// missing dependency is a programming error, not a runtime condition
// worth propagating up the call stack.
func NewImporter(opts ...ImporterOption) *Importer {
	cfg := importerConfig{clock: RealClock{}, logger: slog.Default()}

	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.kernel == nil {
		panic("app.NewImporter: ImporterKernel is required (use WithImporterKernel)")
	}

	if cfg.events == nil {
		panic("app.NewImporter: KernelEvents is required (use WithImporterEvents)")
	}

	if cfg.transport == nil {
		panic("app.NewImporter: Transport is required (use WithImporterTransport)")
	}

	if cfg.codec == nil {
		panic("app.NewImporter: ProtocolCodec is required (use WithImporterCodec)")
	}

	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}

	// NewImporter has no error return, matching the existing missing-
	// dependency contract. Surface validation failures the same way so
	// a caller wiring negative values cannot build a working Importer
	// that quietly ignores them.
	transportErr := ValidateTransportOptions(cfg.transportOptions)
	if transportErr != nil {
		panic(fmt.Errorf("app.NewImporter: TransportOptions invalid: %w", transportErr))
	}

	return &Importer{
		kernel:           cfg.kernel,
		events:           cfg.events,
		transport:        cfg.transport,
		codec:            cfg.codec,
		clock:            cfg.clock,
		logger:           cfg.logger,
		transportOptions: cfg.transportOptions,
		handles:          make(map[domain.PortID]*portHandle),
		inFlight:         make(map[attachKey]struct{}),
	}
}

// Close cancels every registered handle's context, waits for any
// background goroutines to drain, and marks the Importer closed.
// Subsequent Close calls are no-ops via sync.Once.
//
// Close's wait is bounded by the longest shutdownTimeout across the
// registered handles (see [AttachOptions.ShutdownTimeout]): on timer
// fire, Close returns even if the waitgroup has not drained. This is
// a WALL-CLOCK bound only. Any in-flight wg-tracked goroutines —
// reconnect watchers stuck inside kernel.AttachRemote, blocking
// OnReconnect callbacks, or detach goroutines waiting on
// kernel.DetachPort — may continue running past Close's return and
// will be cleaned up when they naturally unwind. Callers who require
// synchronous shutdown must either (a) use a negative shutdownTimeout
// to request an unbounded wait, or (b) ensure their workloads honour
// ctx cancellation.
//
// The internal waiter goroutine spawned by waitGroupBounded does not
// observe the bound itself: it parks on sync.WaitGroup.Wait and exits
// only when the wg drains. When Close returns on timeout with
// uncompleted goroutines, that waiter also lingers until the wg
// eventually clears — this is accepted as the cost of bounded Close
// (sync.WaitGroup cannot be cancelled from outside). In practice this
// means the waiter's lifetime matches the stuck workload's.
//
// The handle map is NOT nilled here: a concurrent Attach may be parked
// past AttachRemote but before registerHandle, and nilling the map
// under it would panic on the unconditional write. Instead,
// registerHandle itself rejects writes once closed is true. The map
// becomes garbage when the *Importer is collected.
func (i *Importer) Close() error {
	i.closeOnce.Do(func() {
		i.mu.Lock()

		i.closed = true

		handles := make([]*portHandle, 0, len(i.handles))
		for _, h := range i.handles {
			handles = append(handles, h)
		}

		i.mu.Unlock()

		// Cancel outside the write lock: cancel funcs may try to
		// re-enter the Importer (e.g. future reconnect watcher
		// acquiring the RLock to check closed) and we must not hold
		// the write lock while doing so.
		for _, h := range handles {
			h.cancel()
		}

		i.waitGroupBounded(handles)

		// Drop every Watch subscriber so iterators terminate cleanly.
		// Done after waitGroupBounded so any still-running reconnect
		// watcher that publishes its terminal exhaustion event has a
		// live subscriber list to land on.
		i.closeAllImporterSubscribers()
	})

	return nil
}

// ListRemote dials endpoint, requests the remote device list via
// OP_REQ_DEVLIST, and returns the decoded []domain.Device. The TCP
// connection is owned for the entire call: it is always closed before
// ListRemote returns (success or failure). OP_REP_DEVLIST does not
// involve fd-passing, so the v1 contract §5.4 handoff contract does not apply
// here — the connection is a short-lived query channel.
//
// Returned errors are wrapped with the peer endpoint so callers can
// distinguish which remote produced the failure when a consumer lists
// across multiple peers.
func (i *Importer) ListRemote(ctx context.Context, endpoint domain.RemoteEndpoint) ([]domain.Device, error) {
	i.mu.RLock()

	closed := i.closed

	i.mu.RUnlock()

	if closed {
		return nil, ErrImporterClosed
	}

	err := endpoint.Validate()
	if err != nil {
		return nil, fmt.Errorf("list remote: %w", err)
	}

	endpoint = endpoint.NormalizePort()

	conn, err := i.transport.Dial(ctx, endpoint, i.transportOptions)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", endpoint.String(), err)
	}

	defer closeConnLogging(conn, i.logger)

	reqBytes := i.codec.EncodeOpReqDevlist()

	_, err = conn.Write(reqBytes)
	if err != nil {
		return nil, fmt.Errorf("write OP_REQ_DEVLIST to %s: %w", endpoint.String(), err)
	}

	devs, err := i.codec.DecodeOpRepDevlist(conn)
	if err != nil {
		return nil, fmt.Errorf("decode OP_REP_DEVLIST from %s: %w", endpoint.String(), err)
	}

	return devs, nil
}

// closeConnLogging closes conn and logs any error via logger. The close
// error is NEVER surfaced to the caller because the primary operation
// has already completed; a close failure is informational only.
func closeConnLogging(conn net.Conn, logger *slog.Logger) {
	err := conn.Close()
	if err != nil {
		logger.Warn("close importer conn", slog.Any("err", err))
	}
}

// deviceIDBusShift mirrors pkg/domain's internal constant: the bit
// offset applied to the busnum field when packing a DeviceID. Kept as
// a local constant so this file does not depend on an exported helper
// that the domain package does not provide.
const deviceIDBusShift = 16

// Attach runs the full USB/IP import sequence per v1 contract §5.2:
//
//  1. kernel.ModulesAvailable probes vhci_hcd + usbip_core.
//  2. transport.Dial establishes the TCP connection to endpoint.
//  3. codec.EncodeOpReqImport(conn, busID) writes the request.
//  4. codec.DecodeOpRepImport(conn) reads back the device body.
//  5. kernel.AttachRemote(ctx, conn, spec) hands the fd to the kernel.
//
// Step 5 is the fd-passing handoff defined in v1 contract §5.4 item 4. Until
// AttachRemote returns success, Attach owns the conn and MUST close it
// on any error path. After success, the kernel owns the fd and Attach
// MUST NOT touch it — closing it there would tear down the just-opened
// vhci port. The local `handedOff` flag implements that split: the
// deferred cleanup is a no-op once handedOff flips to true.
//
// When AttachOptions.AutoReconnect is set, the successful-return path
// also spawns a reconnect watcher goroutine bound to the fresh handle
// (v1 contract §5.5). The watcher is enrolled in i.wg so Close drains it.
func (i *Importer) Attach(
	ctx context.Context,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
	opts AttachOptions,
) (domain.Port, error) {
	err := endpoint.Validate()
	if err != nil {
		return domain.Port{}, fmt.Errorf("attach: %w", err)
	}

	if !busID.IsValid() {
		// Mirror the boundary guard on the exporter side
		// (Exporter.Bind/Unbind): library callers that bypass
		// ParseBusID by raw string conversion must not be allowed
		// to drive a malformed busid into the OP_REQ_IMPORT body
		// or the kernel attach sysfs writes that follow.
		return domain.Port{}, fmt.Errorf("attach %q: %w", busID, domain.ErrBusIDInvalid)
	}

	if opts.MaxAttempts < 0 {
		return domain.Port{}, fmt.Errorf("%w: MaxAttempts %d must be non-negative (0 means infinite)",
			ErrAttachOptionsInvalid, opts.MaxAttempts)
	}

	endpoint = endpoint.NormalizePort()

	release, err := i.acquireAttachSlot(endpoint, busID)
	if err != nil {
		return domain.Port{}, err
	}

	defer release()

	err = i.kernel.ModulesAvailable(ctx)
	if err != nil {
		i.logger.Warn("attach kernel modules unavailable",
			slog.Any("busid", busID),
			slog.String("remote", endpoint.String()),
			slog.String("outcome", string(AttachOutcomeKernelError)),
			slog.Any("err", err))

		return domain.Port{}, fmt.Errorf("vhci modules unavailable: %w", err)
	}

	// attachOverDialed logs the outcome at each failure branch
	// (dial / kernel / decode) so the classification lives with the
	// error origin. On success, finishAttach logs the OK outcome.
	return i.attachOverDialed(ctx, endpoint, busID, opts)
}

// Detach tears down a previously-imported port by id. It cancels the
// handle's context BEFORE issuing the sysfs-backed detach per v1 contract §5.5
// so any auto-reconnect watcher sees cancel ahead of the status
// transition and does not race a reattempt, and blocks on the watcher
// goroutine's done channel before touching the kernel so an in-flight
// reconnect attempt cannot overlap with the sysfs write. When the
// kernel rejects the detach, the handle is left registered so callers
// can retry.
//
// Detach sets handle.detaching BEFORE cancel so a reconnect watcher
// wedged inside kernel.AttachRemote past the bounded wait cannot
// silently register a fresh handle after Detach returns. The watcher
// observes the flag on its post-Attach check and rolls back the kernel
// handoff instead of taking ownership of the replacement port.
func (i *Importer) Detach(ctx context.Context, id domain.PortID) error {
	i.mu.Lock()

	if i.closed {
		i.mu.Unlock()

		return ErrImporterClosed
	}

	h, ok := i.handles[id]
	if !ok {
		i.mu.Unlock()

		i.logger.Warn("importer detach unknown port",
			slog.Any("port_id", id),
			slog.String("outcome", string(DetachOutcomeNotFound)))

		return fmt.Errorf("detach port %d: %w", id, domain.ErrDeviceNotBound)
	}

	// Enrol the kernel-side detach in the waitgroup BEFORE releasing
	// the lock. Close acquires the lock, flips closed=true, then waits
	// on i.wg — so incrementing here guarantees Close observes the
	// in-flight detach and blocks until it drains, closing the window
	// where Close could return while sysfs writes are still in-flight.
	i.wg.Add(1)
	defer i.wg.Done()

	// Mark the handle as detaching BEFORE releasing the lock so a
	// concurrent watcher reading the flag cannot observe it unset after
	// the post-Attach check. Pairing the store with the mu-protected
	// lookup makes the happens-before explicit: any watcher holding
	// the RLock later will see the flag set.
	h.detaching.Store(true)

	i.mu.Unlock()

	// Cancel first (v1 contract §5.5) so any reconnect watcher observes
	// termination and exits. Waiting on watcherDone guarantees the
	// watcher has drained before DetachPort runs; a nil watcherDone
	// means this handle was attached with AutoReconnect=false. The
	// wait is bounded by the handle's shutdownTimeout: a wedged watcher
	// (e.g. a kernel call ignoring ctx) cannot hang Detach indefinitely.
	h.cancel()

	if h.watcherDone != nil {
		i.waitWatcherBounded(h, id)
	}

	err := i.kernel.DetachPort(ctx, id)
	if err != nil {
		// Preserve the handle so callers can retry; the cancelled
		// context is harmless — any future watcher starts fresh from
		// the next successful Attach which regenerates the handle.
		i.logger.Warn("importer detach kernel error",
			slog.Any("port_id", id),
			slog.String("outcome", string(DetachOutcomeError)),
			slog.Any("err", err))

		return fmt.Errorf("detach port %d: %w", id, err)
	}

	i.mu.Lock()
	delete(i.handles, id)
	i.mu.Unlock()

	i.logger.Info("importer detached",
		slog.Any("port_id", id),
		slog.String("outcome", string(DetachOutcomeOK)))

	return nil
}

// ListPorts forwards to the kernel's view of attached vhci ports. The
// Importer's local handle map is internal bookkeeping; the kernel's
// sysfs-derived list is the authoritative source, especially after a
// daemon restart (§5.4 item 7) where our handles are empty but the
// kernel still tracks live ports.
func (i *Importer) ListPorts(ctx context.Context) ([]domain.Port, error) {
	i.mu.RLock()

	closed := i.closed

	i.mu.RUnlock()

	if closed {
		return nil, ErrImporterClosed
	}

	ports, err := i.kernel.ListPorts(ctx)
	if err != nil {
		return nil, fmt.Errorf("list vhci ports: %w", err)
	}

	return ports, nil
}

// Watch returns an iter.Seq that yields domain events from the shared
// KernelEvents source. Iteration terminates when any of the following
// happens: the source channel closes, ctx is cancelled, the caller's
// yield returns false (normal `break`), or Subscribe fails (in which
// case the iter yields nothing and terminates immediately).
//
// Post-Close Watch returns an iter that yields nothing and terminates
// immediately — the handle map is already torn down and there is no
// upstream to bind to.
//
// Subscriber registration and the upstream KernelEvents.Subscribe call
// are deferred until the consumer ranges over the returned iter so a
// caller that constructs the iter and then drops it does not leak a
// kernel subscription handle or a fanout slot. The closed-Importer
// fast path stays eager because there is no resource to defer in that
// case.
func (i *Importer) Watch(ctx context.Context) iter.Seq[domain.Event] {
	i.mu.RLock()

	closed := i.closed

	i.mu.RUnlock()

	if closed {
		return emptyEventSeq
	}

	return func(yield func(domain.Event) bool) {
		i.mu.Lock()
		if i.closed {
			i.mu.Unlock()

			return
		}

		sub := &importerEventSubscriber{
			ch:   make(chan domain.Event, importerEventBufSize),
			done: make(chan struct{}),
		}

		i.subscribers = append(i.subscribers, sub)
		i.mu.Unlock()

		ch, cancel, err := i.events.Subscribe(ctx)
		if err != nil {
			i.logger.Warn("watch subscribe failed", slog.Any("err", err))
			i.removeImporterSubscriber(sub)

			return
		}

		i.runImporterMergedSeq(ctx, ch, cancel, sub, yield)
	}
}

// waitWatcherBounded blocks on h.watcherDone up to h.shutdownTimeout,
// then logs and returns so Detach can proceed with the kernel-side
// detach regardless. A negative shutdownTimeout disables the bound.
func (i *Importer) waitWatcherBounded(h *portHandle, id domain.PortID) {
	if h.shutdownTimeout < 0 {
		<-h.watcherDone

		return
	}

	select {
	case <-h.watcherDone:
	case <-i.clock.After(h.shutdownTimeout):
		i.logger.Warn("detach watcher wait timed out",
			slog.Any("port_id", id),
			slog.Duration("timeout", h.shutdownTimeout),
		)
	}
}

// waitGroupBounded waits on i.wg up to the longest shutdownTimeout
// across the registered handles, then logs and returns so Close can
// release the caller regardless of a wedged background goroutine. A
// negative timeout on any handle opts the whole Close into unbounded
// wait, matching the per-Detach semantics of waitWatcherBounded.
func (i *Importer) waitGroupBounded(handles []*portHandle) {
	timeout := longestShutdownTimeout(handles)

	if timeout < 0 {
		i.wg.Wait()

		return
	}

	done := make(chan struct{})

	go func() {
		i.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-i.clock.After(timeout):
		i.logger.Warn("close waitgroup drain timed out",
			slog.Duration("timeout", timeout),
		)
	}
}

// longestShutdownTimeout returns the largest shutdownTimeout among the
// supplied handles, or the §5.5 default when the slice is empty. A
// negative on any handle poisons the result to -1 (wait forever).
// registerHandle normalises zero to the default before storing, so the
// "every handle has zero" branch is unreachable in production; the
// default-on-empty branch remains for Close's cancel-nothing case.
func longestShutdownTimeout(handles []*portHandle) time.Duration {
	if len(handles) == 0 {
		return defaultShutdownTimeout
	}

	longest := time.Duration(0)

	for _, h := range handles {
		if h.shutdownTimeout < 0 {
			return -1
		}

		if h.shutdownTimeout > longest {
			longest = h.shutdownTimeout
		}
	}

	return longest
}

// acquireAttachSlot serialises concurrent Attach calls for the same
// (endpoint, busid) pair. Returns ErrImporterClosed when the
// importer has already shut down, ErrAttachInProgress when another
// Attach for this key is still running, or a release func the caller
// MUST invoke on every return path to free the slot. The check +
// insert + wg.Add happen under i.mu so Close observes every
// in-flight Attach via waitGroupBounded: an Attach that gets past
// the closed check has incremented wg, and a Close that sets closed
// first blocks the wg Add from ever reaching Attach's body.
func (i *Importer) acquireAttachSlot(
	endpoint domain.RemoteEndpoint, busID domain.BusID,
) (func(), error) {
	key := attachKey{remote: endpoint, busID: busID}

	i.mu.Lock()
	defer i.mu.Unlock()

	if i.closed {
		return nil, ErrImporterClosed
	}

	if _, busy := i.inFlight[key]; busy {
		return nil, fmt.Errorf("%w: %s on %s",
			ErrAttachInProgress, busID, endpoint.String())
	}

	i.inFlight[key] = struct{}{}
	i.wg.Add(1)

	return func() {
		i.mu.Lock()
		delete(i.inFlight, key)
		i.mu.Unlock()
		i.wg.Done()
	}, nil
}

// classifyDecodeImportErr maps a DecodeOpRepImport failure onto the
// closest §11.5.5 AttachOutcome label. A non-zero OP_REP_IMPORT
// status is a domain-level peer rejection (one of ST_NA, ST_NODEV,
// ST_DEV_BUSY, ST_DEV_ERR per upstream usbip_common.h) — NOT a
// wire framing fault. Any of those rejections must be classified
// as kernel_error so observability does not over-count
// protocol_mismatch when remote daemons are simply busy or
// reporting device errors. Genuine wire decode failures (header
// parse, device-body underrun) remain protocol_mismatch.
//
// The closed-set outcome label still stays kernel_error for all
// peer rejections because the v1 contract §11.5.5 enum does not
// yet split "rejected" from "kernel_error"; what matters is that
// errors.Is on the returned Attach error reaches the precise
// sentinel (NotFound, AlreadyBound, Unavailable) so callers can
// distinguish.
func classifyDecodeImportErr(err error) AttachOutcome {
	if errors.Is(err, domain.ErrDeviceNotFound) ||
		errors.Is(err, domain.ErrDeviceAlreadyBound) ||
		errors.Is(err, domain.ErrDeviceUnavailable) {
		return AttachOutcomeKernelError
	}

	return AttachOutcomeProtocolMismatch
}

// classifyKernelAttachErr maps an AttachRemote (kernel handoff) error
// onto the closed AttachOutcome set. ErrPermission surfaces when
// sysfs writes to /sys/bus/usb/.../usbip_sockfd or attach require
// CAP_SYS_ADMIN; ErrNoFreePort surfaces when every vhci slot is
// taken. Anything else falls through to kernel_error so the
// closed-set contract is preserved without a catch-all.
func classifyKernelAttachErr(err error) AttachOutcome {
	switch {
	case errors.Is(err, domain.ErrPermission):
		return AttachOutcomePermission
	case errors.Is(err, domain.ErrNoFreePort):
		return AttachOutcomeNoFreePort
	}

	return AttachOutcomeKernelError
}

// attachOverDialed factors out the dial-through-handoff portion of
// Attach. Splitting it keeps Attach under the project's cyclomatic cap
// and isolates the fd-passing deferred cleanup per v1 contract §5.4. opts is
// forwarded unchanged so registerHandle can hand the resulting handle
// to a reconnect watcher when AutoReconnect is enabled.
func (i *Importer) attachOverDialed(
	ctx context.Context,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
	opts AttachOptions,
) (domain.Port, error) {
	i.logger.Debug("attach: dialing", "endpoint", endpoint.String(), "busid", busID)

	conn, err := i.transport.Dial(ctx, endpoint, i.transportOptions)
	if err != nil {
		i.logAttachFailure("attach dial failed", busID, endpoint, AttachOutcomeDialFailed, err)

		return domain.Port{}, fmt.Errorf("dial %s: %w", endpoint.String(), err)
	}

	i.logger.Debug("attach: dialed", "endpoint", endpoint.String(), "local", conn.LocalAddr().String())

	// Per v1 contract §5.4 item 4: Attach owns the fd until AttachRemote
	// succeeds. The deferred close below runs on every return; the
	// handedOff flag suppresses it exactly once, right after the
	// kernel takes ownership.
	handedOff := false

	defer func() {
		if !handedOff {
			closeConnLogging(conn, i.logger)
		}
	}()

	i.logger.Debug("attach: sending OP_REQ_IMPORT", "busid", busID)

	err = i.codec.EncodeOpReqImport(conn, busID)
	if err != nil {
		i.logAttachFailure("attach encode handshake failed", busID, endpoint, AttachOutcomeProtocolMismatch, err)

		return domain.Port{}, fmt.Errorf("encode OP_REQ_IMPORT for %s: %w", busID, err)
	}

	i.logger.Debug("attach: awaiting OP_REP_IMPORT")

	dev, err := i.codec.DecodeOpRepImport(conn)
	if err != nil {
		i.logAttachFailure("attach decode handshake failed", busID, endpoint, classifyDecodeImportErr(err), err)

		return domain.Port{}, fmt.Errorf("decode OP_REP_IMPORT from %s: %w", endpoint.String(), err)
	}

	// Wire-side BusID acceptance: the remote sends a BusID we hand
	// straight to the kernel via sysfs paths; a malicious or buggy
	// peer could embed bytes that escape a sysfs basename. The wire
	// codec is intentionally permissive (any padded string), so the
	// app layer applies ValidateWireBusID at the trust boundary.
	_, wireBusIDErr := domain.ValidateWireBusID(string(dev.BusID))
	if wireBusIDErr != nil {
		i.logAttachFailure("attach decode handshake failed",
			busID, endpoint, AttachOutcomeProtocolMismatch, wireBusIDErr)

		return domain.Port{}, fmt.Errorf("decode OP_REP_IMPORT from %s: %w",
			endpoint.String(), wireBusIDErr)
	}

	i.logger.Debug("attach: got OP_REP_IMPORT",
		"busid", dev.BusID, "vid", dev.VendorID, "pid", dev.ProductID, "speed", dev.Speed.String())

	devID := domain.DeviceID((uint32(dev.BusNum) << deviceIDBusShift) | uint32(dev.DevNum))

	spec := RemoteDeviceSpec{
		Device: dev,
		DevID:  devID,
		Speed:  dev.Speed,
		Remote: endpoint,
	}

	portID, err := i.kernel.AttachRemote(ctx, conn, spec)
	if err != nil {
		i.logAttachFailure("attach kernel handoff failed", busID, endpoint, classifyKernelAttachErr(err), err)

		return domain.Port{}, fmt.Errorf("attach %s on %s: %w", busID, endpoint.String(), err)
	}

	handedOff = true

	return i.finishAttach(ctx, portID, busID, endpoint, dev, devID, opts)
}

// logAttachFailure emits the structured "attach failed" record shared
// by every terminal failure branch in attachOverDialed. Centralising
// the field set keeps journald queries consistent and the parent
// function under the project funlen cap.
func (i *Importer) logAttachFailure(
	msg string,
	busID domain.BusID,
	endpoint domain.RemoteEndpoint,
	outcome AttachOutcome,
	err error,
) {
	i.logger.Warn(msg,
		slog.Any("busid", busID),
		slog.String("remote", endpoint.String()),
		slog.String("outcome", string(outcome)),
		slog.Any("err", err))
}

// finishAttach performs the post-handoff bookkeeping: registers the
// port in the handle map, spawns the reconnect watcher when enabled,
// and emits the success metric. Extracted from attachOverDialed so the
// parent function stays under the project funlen cap.
func (i *Importer) finishAttach(
	ctx context.Context,
	portID domain.PortID,
	busID domain.BusID,
	endpoint domain.RemoteEndpoint,
	dev domain.Device,
	devID domain.DeviceID,
	opts AttachOptions,
) (domain.Port, error) {
	h, err := i.registerHandle(portID, busID, endpoint,
		resolveShutdownTimeout(opts.ShutdownTimeout), opts.AutoReconnect)
	if err != nil {
		// Importer closed between AttachRemote and registerHandle.
		// We hold a live kernel port that no handle tracks, so
		// Close's sweep cannot reach it. Best-effort release; log
		// any secondary error so it is not silent, but surface the
		// original ErrImporterClosed to the caller.
		detachErr := i.kernel.DetachPort(ctx, portID)
		if detachErr != nil {
			i.logger.Warn("release port after close race",
				slog.Any("port_id", portID),
				slog.Any("err", detachErr),
			)
		}

		i.logger.Warn("attach lost to importer close race",
			slog.Any("port_id", portID),
			slog.Any("busid", busID),
			slog.String("outcome", string(AttachOutcomeKernelError)),
			slog.Any("err", err))

		return domain.Port{}, err
	}

	port := domain.Port{
		ID:       portID,
		Status:   domain.StatusUsed,
		Speed:    dev.Speed,
		DeviceID: devID,
		Remote:   endpoint,
		BusID:    busID,
	}

	i.mu.Lock()
	h.lastKnownPort = port
	i.mu.Unlock()

	if opts.AutoReconnect {
		i.spawnReconnectWatcher(ctx, h, portID, endpoint, busID, opts)
	}

	i.logger.Info("importer attached",
		slog.Any("port_id", portID),
		slog.Any("busid", busID),
		slog.String("remote", endpoint.String()),
		slog.String("outcome", string(AttachOutcomeOK)))

	return port, nil
}

// registerHandle records a successful attach in the handle map. If an
// entry already existed for this PortID (the kernel re-used the slot
// after a previous detach we didn't observe), its cancel func fires
// first so any in-flight consumer sees termination before the new
// entry appears.
//
// Returns the fresh *portHandle so the caller can spawn a reconnect
// watcher bound to it, or ErrImporterClosed when the Importer was
// closed between AttachRemote's successful return and this register
// call. The caller MUST release the kernel-owned port it just acquired
// on the closed path because no handle was recorded and Close's cancel
// sweep cannot reach it.
func (i *Importer) registerHandle(
	id domain.PortID,
	busID domain.BusID,
	endpoint domain.RemoteEndpoint,
	shutdownTimeout time.Duration,
	autoReconnect bool,
) (*portHandle, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.closed {
		return nil, ErrImporterClosed
	}

	if old, ok := i.handles[id]; ok {
		old.cancel()
	}

	i.nextGen++

	h := &portHandle{
		done:            make(chan struct{}),
		busID:           busID,
		remote:          endpoint,
		generation:      i.nextGen,
		shutdownTimeout: shutdownTimeout,
	}

	// Initialise the watcher-done channel under mu so Detach (which
	// acquires mu before reading the handle) never races the write.
	// Non-AutoReconnect handles leave watcherDone nil; the watcher
	// goroutine, when spawned, closes the channel on exit so Detach's
	// bounded wait unblocks immediately.
	if autoReconnect {
		h.watcherDone = make(chan struct{})
	}

	i.handles[id] = h

	return h, nil
}

// resolveShutdownTimeout maps the user-supplied AttachOptions.ShutdownTimeout
// to its effective value: zero picks up the §5.5 default; any other
// value (including negative) is passed through so callers can disable
// the bound by setting a negative value.
func resolveShutdownTimeout(t time.Duration) time.Duration {
	if t == 0 {
		return defaultShutdownTimeout
	}

	return t
}

// emptyEventSeq is the iter.Seq returned by Watch when there is nothing
// to iterate (Importer closed, Subscribe failed). It terminates
// immediately without invoking yield.
func emptyEventSeq(_ func(domain.Event) bool) {}
