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
// watcher per importer-lifecycle OpenSpec when AttachOptions.ShutdownTimeout is zero. A
// negative ShutdownTimeout disables the bound (wait indefinitely).
const defaultShutdownTimeout = 5 * time.Second

// lifecycleWaitGroup tracks goroutines and exposes a reusable drain
// channel. sync.WaitGroup can only be observed by blocking in Wait, so
// timeout-bounded callers would otherwise need a helper goroutine that
// can outlive the caller when the group never drains.
type lifecycleWaitGroup struct {
	mu      sync.Mutex
	count   int
	drained chan struct{}
}

func (g *lifecycleWaitGroup) Add(delta int) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if delta == 0 {
		return
	}

	if delta > 0 && g.count == 0 {
		g.drained = make(chan struct{})
	}

	next := g.count + delta
	if next < 0 {
		panic("app: negative lifecycleWaitGroup counter")
	}

	g.count = next
	if g.count == 0 {
		close(g.drained)
	}
}

func (g *lifecycleWaitGroup) Done() {
	g.Add(-1)
}

func (g *lifecycleWaitGroup) Go(f func()) {
	g.Add(1)

	go func() {
		defer g.Done()

		f()
	}()
}

func (g *lifecycleWaitGroup) Wait() {
	<-g.DoneChan()
}

func (g *lifecycleWaitGroup) DoneChan() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.drained == nil {
		g.drained = make(chan struct{})
		close(g.drained)
	}

	return g.drained
}

// Importer is the use-case service that imports remote USB devices via
// the vhci_hcd kernel surface. One Importer is sufficient for a whole
// process; construct via NewImporter and release via Close. The zero
// value is not usable — NewImporter initialises required state.
//
// The handle map tracks every successfully-attached port along with a
// per-handle cancel signal and a monotonically increasing generation.
// The reconnect watcher (importer-lifecycle OpenSpec) reads the generation to filter
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
	// reservations bridge the adapter's selected-port callback to handle
	// publication. A reservation exists before kernel mutation begins and
	// is replaced atomically by the matching handle after AttachRemote
	// succeeds. Guarded by mu.
	reservations map[domain.PortID]*attachReservation
	// untrackedDetaches coordinates teardown for kernel-owned ports that this
	// Importer did not attach itself, such as ports inherited after a one-shot
	// CLI attach process exits. Guarded by mu. The kernel mutation, not this
	// process-local map, remains authoritative across Importer instances.
	untrackedDetaches map[domain.PortID]*detachAttempt
	// inFlight dedupes concurrent Attach calls for the same
	// (remote, busid) pair. Without this guard two callers would race
	// the dial + handshake + AttachRemote sequence and import the same
	// device onto two local ports. Guarded by mu.
	inFlight map[attachKey]struct{}
	nextGen  uint64
	wg       lifecycleWaitGroup
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

// attachReservation is the bounded publication state between adapter port
// selection and importer handle registration. Detach marks teardownRequested
// when no handle exists yet, waits on done without holding Importer.mu, and
// leaves that intent behind if its own wait expires so finishAttach can start
// compensating teardown after publishing the handle.
type attachReservation struct {
	id                domain.PortID
	done              chan struct{}
	finishOnce        sync.Once
	shutdownTimeout   time.Duration
	teardownRequested bool // guarded by Importer.mu
	// detachAttempt is published before done closes when a successful
	// handoff must compensate a pre-publication Detach. It is immutable
	// after publication, so every waiter can observe the exact shared result
	// even if the fast compensation removes the handle before it runs again.
	detachAttempt *detachAttempt
}

func (r *attachReservation) finish() {
	r.finishOnce.Do(func() { close(r.done) })
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
// replaced by a successful reattach (importer-lifecycle OpenSpec).
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
	detachMu        sync.Mutex
	detachAttempt   *detachAttempt
	// lastKnownPort is the Port snapshot taken at the most recent
	// successful Attach (initial or reconnect). The reconnect watcher
	// emits this value inside PortReconnectExhaustedEvent when
	// MaxAttempts is reached: the kernel slot is gone by that point,
	// so this is the truthful "last viable" view. Guarded by
	// Importer.mu (writes happen under the importer lock).
	lastKnownPort domain.Port
}

// detachAttempt is the immutable result future shared by callers that
// overlap while detaching one exact portHandle generation.
type detachAttempt struct {
	done chan struct{}
	err  error
}

type detachTarget struct {
	handle      *portHandle
	attempt     *detachAttempt
	reservation *attachReservation
	untracked   bool
	owner       bool
}

// cancel closes the done channel exactly once, signalling any watcher
// to exit. Safe to call repeatedly from different goroutines.
func (h *portHandle) cancel() {
	h.cancelOnce.Do(func() { close(h.done) })
}

func (h *portHandle) acquireDetachAttempt() (*detachAttempt, bool) {
	h.detachMu.Lock()
	defer h.detachMu.Unlock()

	if h.detachAttempt != nil {
		return h.detachAttempt, false
	}

	attempt := &detachAttempt{done: make(chan struct{})}

	h.detachAttempt = attempt

	return attempt, true
}

func (h *portHandle) finishDetachAttempt(attempt *detachAttempt, err error) {
	h.detachMu.Lock()
	defer h.detachMu.Unlock()

	attempt.err = err
	close(attempt.done)

	// A failed attempt is complete for current followers, while a
	// later caller must be able to own a fresh retry.
	if err != nil && h.detachAttempt == attempt {
		h.detachAttempt = nil
	}
}

func waitDetachAttempt(ctx context.Context, id domain.PortID, attempt *detachAttempt) error {
	select {
	case <-attempt.done:
		return attempt.err
	default:
	}

	select {
	case <-attempt.done:
		return attempt.err
	case <-ctx.Done():
	}

	// Completion wins a cancellation tie so every caller that can
	// already observe the shared result receives that result.
	select {
	case <-attempt.done:
		return attempt.err
	default:
		return fmt.Errorf("detach port %d: %w", id, ctx.Err())
	}
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
		kernel:            cfg.kernel,
		events:            cfg.events,
		transport:         cfg.transport,
		codec:             cfg.codec,
		clock:             cfg.clock,
		logger:            cfg.logger,
		transportOptions:  cfg.transportOptions,
		handles:           make(map[domain.PortID]*portHandle),
		reservations:      make(map[domain.PortID]*attachReservation),
		untrackedDetaches: make(map[domain.PortID]*detachAttempt),
		inFlight:          make(map[attachKey]struct{}),
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
// waitGroupBounded observes the Importer's lifecycle wait group through
// a drain channel, so a timeout-bounded Close does not allocate an
// extra goroutine that can linger behind the Close call.
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
// involve fd-passing, so the kernel-adapter and importer-lifecycle OpenSpec documents handoff contract does not apply
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

	// Close conn on ctx cancellation so a blocked decode is interrupted
	// even when ctx carries no deadline (pure cancel).
	watchDone := make(chan struct{})
	defer close(watchDone)

	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-watchDone:
		}
	}()

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
	if err != nil && !errors.Is(err, net.ErrClosed) {
		logger.Warn("close connection", slog.Any("err", err))
	}
}

// deviceIDBusShift mirrors pkg/domain's internal constant: the bit
// offset applied to the busnum field when packing a DeviceID. Kept as
// a local constant so this file does not depend on an exported helper
// that the domain package does not provide.
const deviceIDBusShift = 16

// Attach runs the full USB/IP import sequence per importer-lifecycle OpenSpec:
//
//  1. kernel.ModulesAvailable probes vhci_hcd + usbip_core.
//  2. transport.Dial establishes the TCP connection to endpoint.
//  3. codec.EncodeOpReqImport(conn, busID) writes the request.
//  4. codec.DecodeOpRepImport(conn) reads back the device body.
//  5. kernel.AttachRemote(ctx, conn, spec) hands the fd to the kernel.
//
// Step 5 is the fd-passing handoff defined in the kernel-adapter and importer-lifecycle OpenSpec documents. Until
// AttachRemote returns success, Attach owns the conn and MUST close it
// on any error path. After success, the kernel owns the fd and Attach
// MUST NOT touch it — closing it there would tear down the just-opened
// vhci port. The local `handedOff` flag implements that split: the
// deferred cleanup is a no-op once handedOff flips to true.
//
// When AttachOptions.AutoReconnect is set, the successful-return path
// also spawns a reconnect watcher goroutine bound to the fresh handle
// (importer-lifecycle OpenSpec). The watcher is enrolled in i.wg so Close drains it.
func (i *Importer) Attach(
	ctx context.Context,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
	opts AttachOptions,
) (domain.Port, error) {
	port, _, err := i.attach(ctx, endpoint, busID, opts)

	return port, err
}

// Detach tears down a kernel-owned vhci port by id. When this Importer owns a
// matching handle, it cancels the handle's context BEFORE issuing the
// sysfs-backed detach per importer-lifecycle OpenSpec
// so any auto-reconnect watcher sees cancel ahead of the status
// transition and does not race a reattempt, and blocks on the watcher
// goroutine's done channel before touching the kernel so an in-flight
// reconnect attempt cannot overlap with the sysfs write. When the
// kernel rejects the detach for a reason other than already-free, the handle is
// left registered so callers can retry. An authoritative already-free result
// removes only that exact stale handle so a later Port generation can reuse the
// ID.
//
// Detach sets handle.detaching BEFORE cancel so a reconnect watcher
// wedged inside kernel.AttachRemote past the bounded wait cannot
// silently register a fresh handle after Detach returns. The watcher
// observes the flag on its post-Attach check and rolls back the kernel
// handoff instead of taking ownership of the replacement port.
//
// A fresh Importer has no process-local handle for ports inherited from an
// earlier process. In that case Detach delegates directly to the kernel and
// coordinates overlapping callers in untrackedDetaches. This keeps one-shot
// CLI attach and detach invocations interoperable without a racy ListPorts
// preflight; the serialized kernel mutation decides whether the Port is live.
func (i *Importer) Detach(ctx context.Context, id domain.PortID) error {
	for {
		target, err := i.acquireDetachTarget(id)
		if err != nil {
			return err
		}

		if target.reservation == nil {
			return i.detachPublishedTarget(ctx, id, target)
		}

		attempt, waitErr := i.waitAttachPublication(ctx, id, target.reservation)
		if waitErr != nil {
			return waitErr
		}

		if attempt != nil {
			return waitDetachAttempt(ctx, id, attempt)
		}

		// Reservation completion atomically leaves either a published
		// handle without teardown intent or no attachment. Re-run the
		// lookup to own the former or report the latter truthfully. A
		// teardown-requested publication returns its immutable attempt
		// above, avoiding a successful-fast-compensation/not-found race.
	}
}

// ListPorts forwards to the kernel's view of attached vhci ports. The
// Importer's local handle map is internal bookkeeping; the kernel's
// sysfs-derived list is the authoritative source, especially after a
// daemon restart where our handles are empty but the kernel still owns imported
// sockets and tracks live ports.
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

	i.mu.RLock()
	defer i.mu.RUnlock()

	for index := range ports {
		handle, ok := i.handles[ports[index].ID]
		if !ok {
			continue
		}

		ports[index] = enrichPortFromHandle(ports[index], handle)
	}

	return ports, nil
}

// enrichPortFromHandle overlays only remote identity that the kernel cannot
// retain. Kernel-derived lifecycle and local-topology fields remain
// authoritative, and a reused slot must match the handle's last successful
// DeviceID and speed before receiving process-local metadata.
func enrichPortFromHandle(port domain.Port, handle *portHandle) domain.Port {
	lastKnown := handle.lastKnownPort
	if port.ID != lastKnown.ID ||
		port.Status != domain.StatusUsed ||
		port.DeviceID != lastKnown.DeviceID ||
		port.Speed != lastKnown.Speed {
		return port
	}

	port.Remote = lastKnown.Remote
	port.BusID = lastKnown.BusID

	return port
}

// Watch returns the v1 event-only iterator. It is a compatibility wrapper
// around WatchWithErrors: ordinary events are forwarded, while the first
// terminal stream error ends iteration without changing the historical
// method signature.
func (i *Importer) Watch(ctx context.Context) iter.Seq[domain.Event] {
	return func(yield func(domain.Event) bool) {
		for event, watchErr := range i.WatchWithErrors(ctx) {
			if watchErr != nil || !yield(event) {
				return
			}
		}
	}
}

// WatchWithErrors returns an iter.Seq2 that yields domain events from the
// shared KernelEvents source together with a terminal error channel.
// Subscription failures retain their wrapped cause. An established source
// closing while both ctx and the Importer remain live yields
// ErrEventStreamClosed. Caller cancellation and Importer.Close end cleanly.
//
// Post-Close WatchWithErrors returns an iter that yields nothing and terminates
// immediately — the handle map is already torn down and there is no
// upstream to bind to.
//
// Subscriber registration and the upstream KernelEvents.Subscribe call
// are deferred until the consumer ranges over the returned iter so a
// caller that constructs the iter and then drops it does not leak a
// kernel subscription handle or a fanout slot. The closed-Importer
// fast path stays eager because there is no resource to defer in that
// case.
func (i *Importer) WatchWithErrors(ctx context.Context) iter.Seq2[domain.Event, error] {
	i.mu.RLock()

	closed := i.closed

	i.mu.RUnlock()

	if closed {
		return emptyEventErrorSeq
	}

	return func(yield func(domain.Event, error) bool) {
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

			stopped := i.watchStopped(ctx, sub)
			i.removeImporterSubscriber(sub)

			if stopped {
				return
			}

			_ = yield(nil, fmt.Errorf("subscribe importer events: %w", err))

			return
		}

		i.runImporterMergedSeq(ctx, ch, cancel, sub, yield)
	}
}

// attach runs Attach and returns the exact handle published for a successful
// kernel handoff. Public callers do not need the ownership token, while the
// reconnect path must retain it so rollback cannot rediscover a newer handle
// through a PortID that the kernel has already reused.
func (i *Importer) attach(
	ctx context.Context,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
	opts AttachOptions,
) (domain.Port, *portHandle, error) {
	// A terminal lifecycle state takes precedence over argument validation.
	// acquireAttachSlot repeats this check under the write lock so Close racing
	// this read still prevents the operation from entering the attach body.
	i.mu.RLock()

	closed := i.closed

	i.mu.RUnlock()

	if closed {
		return domain.Port{}, nil, ErrImporterClosed
	}

	err := endpoint.Validate()
	if err != nil {
		return domain.Port{}, nil, fmt.Errorf("attach: %w", err)
	}

	if !busID.IsValid() {
		// Mirror the boundary guard on the exporter side
		// (Exporter.Bind/Unbind): library callers that bypass
		// ParseBusID by raw string conversion must not be allowed
		// to drive a malformed busid into the OP_REQ_IMPORT body
		// or the kernel attach sysfs writes that follow.
		return domain.Port{}, nil, fmt.Errorf("attach %q: %w", busID, domain.ErrBusIDInvalid)
	}

	if opts.MaxAttempts < 0 {
		return domain.Port{}, nil, fmt.Errorf("%w: MaxAttempts %d must be non-negative (0 means infinite)",
			ErrAttachOptionsInvalid, opts.MaxAttempts)
	}

	endpoint = endpoint.NormalizePort()

	release, err := i.acquireAttachSlot(endpoint, busID)
	if err != nil {
		return domain.Port{}, nil, err
	}

	defer release()

	// Construct importer-level custom state only after lifecycle, argument,
	// and deduplication checks have succeeded, but before kernel/network side
	// effects begin. Clearing the factory ensures recursive reconnect Attach
	// calls retain this exact instance instead of creating a new generation.
	if opts.AutoReconnect && opts.Backoff == nil && opts.BackoffFactory != nil {
		opts.Backoff = opts.BackoffFactory()
		opts.BackoffFactory = nil
	}

	err = i.kernel.ModulesAvailable(ctx)
	if err != nil {
		i.logger.Warn("attach kernel modules unavailable",
			slog.Any("busid", busID),
			slog.String("remote", endpoint.String()),
			slog.String("outcome", string(AttachOutcomeKernelError)),
			slog.Any("err", err))

		return domain.Port{}, nil, fmt.Errorf("vhci modules unavailable: %w", err)
	}

	// attachOverDialed logs the outcome at each failure branch
	// (dial / kernel / decode) so the classification lives with the
	// error origin. On success, finishAttach logs the OK outcome.
	return i.attachOverDialed(ctx, endpoint, busID, opts)
}

func (i *Importer) detachPublishedTarget(
	ctx context.Context, id domain.PortID, target detachTarget,
) error {
	if !target.owner {
		return waitDetachAttempt(ctx, id, target.attempt)
	}

	if target.untracked {
		err := i.detachUntrackedPort(ctx, id)
		i.finishUntrackedDetach(id, target.attempt, err)

		return err
	}

	err := i.detachHandle(ctx, id, target.handle)
	target.handle.finishDetachAttempt(target.attempt, err)
	i.wg.Done()

	return err
}

// acquireDetachTarget performs the handle/reservation lookup and any
// ownership transition under Importer.mu. Initial handoff reservations are
// visible before kernel mutation, so the no-handle case can wait for a
// publication instead of mutating a newly-live generation. When neither
// exists, the Port may still be kernel-owned by an earlier process, so callers
// create or join an untracked attempt rather than treating local bookkeeping
// as authoritative.
func (i *Importer) acquireDetachTarget(id domain.PortID) (detachTarget, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.closed {
		return detachTarget{}, ErrImporterClosed
	}

	// A selected-port reservation wins over an older handle that still uses
	// the same PortID. The reconnecting Attach already owns the adapter's
	// mutation boundary, so detaching the old pointer would wait behind that
	// attach and then mutate the newly-live generation. Record compensation
	// against the reservation instead and cancel the predecessor watcher.
	if reservation, ok := i.reservations[id]; ok {
		if predecessor, exists := i.handles[id]; exists {
			predecessor.detaching.Store(true)
			predecessor.cancel()
		}

		// Preserve teardown intent even when this caller's bounded wait
		// later expires. Successful publication will schedule an exact-
		// handle compensating detach; failed handoff simply clears done.
		reservation.teardownRequested = true

		return detachTarget{reservation: reservation}, nil
	}

	if h, ok := i.handles[id]; ok {
		attempt, owner := h.acquireDetachAttempt()
		if owner {
			// Enrol before releasing Importer.mu so Close cannot miss the
			// kernel-side detach during its waitgroup snapshot.
			i.wg.Add(1)
			h.detaching.Store(true)
		}

		return detachTarget{handle: h, attempt: attempt, owner: owner}, nil
	}

	if attempt, ok := i.untrackedDetaches[id]; ok {
		return detachTarget{attempt: attempt, untracked: true}, nil
	}

	attempt := &detachAttempt{done: make(chan struct{})}

	i.untrackedDetaches[id] = attempt
	// Enrol before releasing Importer.mu so Close cannot miss the kernel-side
	// mutation when it snapshots the lifecycle group.
	i.wg.Add(1)

	return detachTarget{attempt: attempt, untracked: true, owner: true}, nil
}

// detachUntrackedPort is the owner-only body for a kernel Port without a
// matching process-local handle. No ListPorts preflight is performed: it would
// race the detach write and could report a stale answer. DetachPort is the
// serialized authoritative operation and classifies an already-free Port as
// ErrDeviceNotBound.
func (i *Importer) detachUntrackedPort(ctx context.Context, id domain.PortID) error {
	err := i.kernel.DetachPort(ctx, id)
	if err != nil {
		outcome := DetachOutcomeError
		if errors.Is(err, domain.ErrDeviceNotBound) {
			outcome = DetachOutcomeNotFound
		}

		i.logger.Warn("importer detach kernel error",
			slog.Any("port_id", id),
			slog.String("outcome", string(outcome)),
			slog.Any("err", err))

		return fmt.Errorf("detach port %d: %w", id, err)
	}

	i.logger.Info("importer detached",
		slog.Any("port_id", id),
		slog.String("outcome", string(DetachOutcomeOK)))

	return nil
}

// finishUntrackedDetach publishes one immutable result to all overlapping
// callers, releases the exact map entry, and drains the lifecycle enrollment.
// Removing successful and failed attempts lets a later non-overlapping caller
// ask the kernel again, which is necessary for both duplicate classification
// and retry after a transient failure.
func (i *Importer) finishUntrackedDetach(
	id domain.PortID, attempt *detachAttempt, err error,
) {
	i.mu.Lock()
	attempt.err = err
	close(attempt.done)

	if current, ok := i.untrackedDetaches[id]; ok && current == attempt {
		delete(i.untrackedDetaches, id)
	}
	i.mu.Unlock()
	i.wg.Done()
}

// waitAttachPublication waits for a selected port to become either a tracked
// handle or an aborted handoff. It never holds Importer.mu, and the effective
// Attach ShutdownTimeout bounds a wedged handoff unless the caller explicitly
// selected a negative (unbounded) timeout. Completion wins cancellation or
// timeout ties so a newly published handle is never mistaken for a timeout.
// A non-nil returned attempt is the immutable compensation future published
// with the handle; the caller must wait it directly rather than re-read the map.
func (i *Importer) waitAttachPublication(
	ctx context.Context, id domain.PortID, reservation *attachReservation,
) (*detachAttempt, error) {
	select {
	case <-reservation.done:
		return reservation.detachAttempt, nil
	default:
	}

	var timeout <-chan time.Time
	if reservation.shutdownTimeout >= 0 {
		timeout = i.clock.After(reservation.shutdownTimeout)
	}

	var waitErr error

	select {
	case <-reservation.done:
		return reservation.detachAttempt, nil
	case <-ctx.Done():
		waitErr = ctx.Err()
	case <-timeout:
		waitErr = context.DeadlineExceeded
	}

	select {
	case <-reservation.done:
		return reservation.detachAttempt, nil
	default:
		return nil, fmt.Errorf("detach port %d waiting for attach publication: %w", id, waitErr)
	}
}

// detachHandle is the owner-only body of a shared detach attempt. It
// waits for the reconnect watcher, then rechecks exact handle identity
// immediately before asking the kernel adapter to mutate the port. A
// superseded handle is already gone from this caller's generation and
// therefore completes without mutating the new owner.
func (i *Importer) detachHandle(ctx context.Context, id domain.PortID, h *portHandle) error {
	// Cancel first (importer-lifecycle OpenSpec) so any reconnect watcher observes
	// termination and exits. Waiting on watcherDone guarantees the
	// watcher has drained before DetachPort runs; a nil watcherDone
	// means this handle was attached with AutoReconnect=false. The
	// wait is bounded by the handle's shutdownTimeout: a wedged watcher
	// (e.g. a kernel call ignoring ctx) cannot hang Detach indefinitely.
	h.cancel()

	if h.watcherDone != nil {
		i.waitWatcherBounded(h, id)
	}

	i.mu.RLock()

	current, stillOurs := i.handles[id]
	i.mu.RUnlock()

	if !stillOurs || current != h {
		return nil
	}

	err := i.kernel.DetachPort(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrDeviceNotBound) {
			i.deleteExactHandle(id, h)
			i.logger.Warn("importer detach found port already released",
				slog.Any("port_id", id),
				slog.String("outcome", string(DetachOutcomeNotFound)),
				slog.Any("err", err))

			return fmt.Errorf("detach port %d: %w", id, err)
		}

		// Preserve the handle so callers can retry; the cancelled
		// context is harmless — any future watcher starts fresh from
		// the next successful Attach which regenerates the handle.
		i.logger.Warn("importer detach kernel error",
			slog.Any("port_id", id),
			slog.String("outcome", string(DetachOutcomeError)),
			slog.Any("err", err))

		return fmt.Errorf("detach port %d: %w", id, err)
	}

	i.deleteExactHandle(id, h)

	i.logger.Info("importer detached",
		slog.Any("port_id", id),
		slog.String("outcome", string(DetachOutcomeOK)))

	return nil
}

func (i *Importer) deleteExactHandle(id domain.PortID, h *portHandle) {
	i.mu.Lock()
	if current, ok := i.handles[id]; ok && current == h {
		delete(i.handles, id)
	}
	i.mu.Unlock()
}

// detachExactHandle owns or joins teardown for one already-published handle.
// Unlike acquireDetachTarget it deliberately remains usable after Importer
// closure: internal rollback is part of draining a reconnect watcher that may
// have crossed Close while its kernel handoff was in progress. The exact
// pointer check and detach-attempt acquisition happen under Importer.mu so a
// concurrent public Detach cannot create a second kernel mutation.
func (i *Importer) detachExactHandle(
	ctx context.Context, id domain.PortID, h *portHandle,
) error {
	i.mu.Lock()

	current, ok := i.handles[id]
	if !ok || current != h {
		i.mu.Unlock()

		return nil
	}

	attempt, owner := h.acquireDetachAttempt()
	if owner {
		i.wg.Add(1)
		h.detaching.Store(true)
	}

	i.mu.Unlock()

	if !owner {
		return waitDetachAttempt(ctx, id, attempt)
	}

	err := i.detachHandle(ctx, id, h)
	h.finishDetachAttempt(attempt, err)
	i.wg.Done()

	return err
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
		i.logger.Warn(
			"detach watcher wait timed out",
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

	done := i.wg.DoneChan()

	select {
	case <-done:
	case <-i.clock.After(timeout):
		i.logger.Warn(
			"close waitgroup drain timed out",
			slog.Duration("timeout", timeout),
		)
	}
}

// longestShutdownTimeout returns the largest shutdownTimeout among the
// supplied handles, or the importer-lifecycle OpenSpec default when the slice is empty. A
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
// closest operations-observability OpenSpec AttachOutcome label. A non-zero OP_REP_IMPORT
// status is a domain-level peer rejection (one of ST_NA, ST_NODEV,
// ST_DEV_BUSY, ST_DEV_ERR per upstream usbip_common.h) — NOT a
// wire framing fault. Any of those rejections must be classified
// as kernel_error so observability does not over-count
// protocol_mismatch when remote daemons are simply busy or
// reporting device errors. Genuine wire decode failures (header
// parse, device-body underrun) remain protocol_mismatch.
//
// The closed-set outcome label still stays kernel_error for all
// peer rejections because the operations-observability OpenSpec enum does not
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
// and isolates the fd-passing deferred cleanup per kernel-adapter and importer-lifecycle OpenSpec documents. opts is
// forwarded unchanged so registerHandle can hand the resulting handle
// to a reconnect watcher when AutoReconnect is enabled.
func (i *Importer) attachOverDialed(
	ctx context.Context,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
	opts AttachOptions,
) (domain.Port, *portHandle, error) {
	i.logger.Debug("attach: dialing", "endpoint", endpoint.String(), "busid", busID)

	conn, err := i.transport.Dial(ctx, endpoint, i.transportOptions)
	if err != nil {
		i.logAttachFailure("attach dial failed", busID, endpoint, AttachOutcomeDialFailed, err)

		return domain.Port{}, nil, fmt.Errorf("dial %s: %w", endpoint.String(), err)
	}

	i.logger.Debug("attach: dialed", "endpoint", endpoint.String(), "local", conn.LocalAddr().String())

	// Per the kernel-adapter and importer-lifecycle OpenSpec documents, Attach owns the fd until AttachRemote
	// succeeds. The deferred close below runs on every return; the
	// handedOff flag suppresses it exactly once, right after the
	// kernel takes ownership.
	handedOff := false

	defer func() {
		if !handedOff {
			closeConnLogging(conn, i.logger)
		}
	}()

	// Close conn on ctx cancellation so a blocked decode is interrupted
	// even when ctx carries no deadline (pure cancel).
	watchDone := make(chan struct{})
	defer close(watchDone)

	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-watchDone:
		}
	}()

	dev, err := i.performImportHandshake(conn, endpoint, busID)
	if err != nil {
		return domain.Port{}, nil, err
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

	var reservation *attachReservation

	spec.ReserveLocalPort = func(id domain.PortID) error {
		var reserveErr error

		reservation, reserveErr = i.reserveAttachPort(
			id, resolveShutdownTimeout(opts.ShutdownTimeout),
		)

		return reserveErr
	}

	portID, err := i.kernel.AttachRemote(ctx, conn, spec)
	if err != nil {
		i.abortAttachReservation(reservation)
		i.logAttachFailure("attach kernel handoff failed", busID, endpoint, classifyKernelAttachErr(err), err)

		return domain.Port{}, nil, fmt.Errorf("attach %s on %s: %w", busID, endpoint.String(), err)
	}

	handedOff = true

	return i.finishAttach(ctx, portID, busID, endpoint, dev, devID, opts, reservation)
}

// reserveAttachPort publishes the adapter-selected port before the kernel
// mutation starts. It intentionally holds Importer.mu only while installing a
// small state object; the potentially wedged sysfs handoff runs after unlock.
func (i *Importer) reserveAttachPort(
	id domain.PortID, shutdownTimeout time.Duration,
) (*attachReservation, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if i.closed {
		return nil, ErrImporterClosed
	}

	if _, exists := i.reservations[id]; exists {
		return nil, fmt.Errorf("%w: local port %d publication already reserved", ErrAttachInProgress, id)
	}

	if _, detaching := i.untrackedDetaches[id]; detaching {
		return nil, fmt.Errorf("%w: local port %d is detaching", ErrAttachInProgress, id)
	}

	// Detach won the per-Port transition before this reservation callback.
	// Reject before the adapter's sysfs mutation rather than allowing the
	// replacement to become live behind the already-claimed old generation.
	if current, exists := i.handles[id]; exists && current.detaching.Load() {
		return nil, fmt.Errorf("%w: local port %d is detaching", ErrAttachInProgress, id)
	}

	reservation := &attachReservation{
		id:              id,
		done:            make(chan struct{}),
		shutdownTimeout: shutdownTimeout,
	}

	i.reservations[id] = reservation

	return reservation, nil
}

// abortAttachReservation resolves a reservation after a pre-handoff failure.
// Exact-pointer removal prevents an obsolete failure from clearing a newer
// reservation if a broken ImporterKernel implementation reuses callbacks.
func (i *Importer) abortAttachReservation(reservation *attachReservation) {
	if reservation == nil {
		return
	}

	i.mu.Lock()
	if current, ok := i.reservations[reservation.id]; ok && current == reservation {
		delete(i.reservations, reservation.id)
	}
	i.mu.Unlock()

	reservation.finish()
}

// performImportHandshake runs the OP_REQ_IMPORT / OP_REP_IMPORT
// exchange on conn: send the request, decode the reply, and validate both the
// wire-side BusID encoding and the requested-vs-replied BusID match. Read
// deadlines belong to the transport adapter; the caller's cancellation watcher
// closes conn to interrupt blocked I/O without replacing a tighter configured
// deadline. Extracted from attachOverDialed to keep that function under the
// cognitive-complexity cap.
func (i *Importer) performImportHandshake(
	conn net.Conn,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
) (domain.Device, error) {
	i.logger.Debug("attach: sending OP_REQ_IMPORT", "busid", busID)

	err := i.codec.EncodeOpReqImport(conn, busID)
	if err != nil {
		i.logAttachFailure("attach encode handshake failed", busID, endpoint, AttachOutcomeProtocolMismatch, err)

		return domain.Device{}, fmt.Errorf("encode OP_REQ_IMPORT for %s: %w", busID, err)
	}

	i.logger.Debug("attach: awaiting OP_REP_IMPORT")

	dev, err := i.codec.DecodeOpRepImport(conn)
	if err != nil {
		i.logAttachFailure("attach decode handshake failed", busID, endpoint, classifyDecodeImportErr(err), err)

		return domain.Device{}, fmt.Errorf("decode OP_REP_IMPORT from %s: %w", endpoint.String(), err)
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

		return domain.Device{}, fmt.Errorf("decode OP_REP_IMPORT from %s: %w",
			endpoint.String(), wireBusIDErr)
	}

	// BusID match: reject any reply that names a different device than
	// the one we asked for. Without this check a misbehaving exporter
	// could attach the wrong USB device under the caller's requested
	// handle (the kernel would happily wire the returned dev_id to the
	// vhci port; the caller would then control a device they did not
	// authorize). wire-protocol OpenSpec: the reply MUST identify the same
	// busid the request named.
	if dev.BusID != busID {
		mismatchErr := fmt.Errorf("%w: requested busid %s but peer replied %s",
			domain.ErrProtocolError, busID, dev.BusID)

		i.logAttachFailure("attach decode handshake failed",
			busID, endpoint, AttachOutcomeProtocolMismatch, mismatchErr)

		return domain.Device{}, fmt.Errorf("decode OP_REP_IMPORT from %s: %w",
			endpoint.String(), mismatchErr)
	}

	return dev, nil
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
// and emits the success outcome. Extracted from attachOverDialed so the
// parent function stays under the project funlen cap.
func (i *Importer) finishAttach(
	ctx context.Context,
	portID domain.PortID,
	busID domain.BusID,
	endpoint domain.RemoteEndpoint,
	dev domain.Device,
	devID domain.DeviceID,
	opts AttachOptions,
	reservation *attachReservation,
) (domain.Port, *portHandle, error) {
	h, compensation, err := i.registerHandle(portID, busID, endpoint,
		resolveShutdownTimeout(opts.ShutdownTimeout), opts.AutoReconnect, reservation)
	if err != nil {
		i.abortAttachReservation(reservation)

		// Importer closed between AttachRemote and registerHandle.
		// We hold a live kernel port that no handle tracks, so
		// Close's sweep cannot reach it. Best-effort release; log
		// any secondary error so it is not silent, but surface the
		// original ErrImporterClosed to the caller.
		detachErr := i.kernel.DetachPort(context.WithoutCancel(ctx), portID)
		if detachErr != nil {
			i.logger.Warn(
				"release port after close race",
				slog.Any("port_id", portID),
				slog.Any("err", detachErr),
			)
		}

		i.logger.Warn("attach lost to importer close race",
			slog.Any("port_id", portID),
			slog.Any("busid", busID),
			slog.String("outcome", string(AttachOutcomeKernelError)),
			slog.Any("err", err))

		return domain.Port{}, nil, err
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

	// A reconnect-path Attach shares its strategy with the predecessor and
	// replacement watchers. Reset on this completing watcher goroutine before
	// the replacement watcher becomes observable, otherwise an immediate
	// second detach can race the new watcher's Next against this Reset.
	if compensation == nil && opts.resetBackoffOnSuccess && opts.Backoff != nil {
		opts.Backoff.Reset()
	}

	if opts.AutoReconnect && compensation == nil {
		i.spawnReconnectWatcher(ctx, h, portID, endpoint, busID, opts)
	}

	if compensation != nil {
		i.startCompensatingDetach(ctx, portID, h, compensation)
	}

	i.logger.Info("importer attached",
		slog.Any("port_id", portID),
		slog.Any("busid", busID),
		slog.String("remote", endpoint.String()),
		slog.String("outcome", string(AttachOutcomeOK)))

	return port, h, nil
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
	reservation *attachReservation,
) (*portHandle, *detachAttempt, error) {
	i.mu.Lock()

	reservationErr := i.validateAttachReservationLocked(id, reservation)
	if reservationErr != nil {
		i.mu.Unlock()

		return nil, nil, reservationErr
	}

	if i.closed {
		i.removeAttachReservationLocked(id, reservation)
		i.mu.Unlock()
		completeAttachReservation(reservation)

		return nil, nil, ErrImporterClosed
	}

	compensate := reservation != nil && reservation.teardownRequested
	h := i.newPortHandleLocked(id, busID, endpoint, shutdownTimeout, autoReconnect && !compensate)

	i.handles[id] = h

	compensation := i.prepareAttachCompensationLocked(h, reservation)
	i.removeAttachReservationLocked(id, reservation)
	i.mu.Unlock()
	completeAttachReservation(reservation)

	return h, compensation, nil
}

func (i *Importer) validateAttachReservationLocked(
	id domain.PortID, reservation *attachReservation,
) error {
	if reservation == nil {
		return nil
	}

	current, ok := i.reservations[id]
	if !ok || current != reservation {
		return fmt.Errorf("%w: port %d", errAttachPublicationReservationLost, id)
	}

	return nil
}

func (i *Importer) removeAttachReservationLocked(
	id domain.PortID, reservation *attachReservation,
) {
	if reservation != nil {
		delete(i.reservations, id)
	}
}

func completeAttachReservation(reservation *attachReservation) {
	if reservation != nil {
		reservation.finish()
	}
}

func (i *Importer) newPortHandleLocked(
	id domain.PortID,
	busID domain.BusID,
	endpoint domain.RemoteEndpoint,
	shutdownTimeout time.Duration,
	autoReconnect bool,
) *portHandle {
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

	return h
}

func (i *Importer) prepareAttachCompensationLocked(
	h *portHandle, reservation *attachReservation,
) *detachAttempt {
	if reservation == nil || !reservation.teardownRequested {
		return nil
	}

	compensation, _ := h.acquireDetachAttempt()
	h.detaching.Store(true)
	i.wg.Add(1)

	reservation.detachAttempt = compensation

	return compensation
}

// startCompensatingDetach guarantees that a Detach whose bounded publication
// wait expired is not forgotten. The exact published handle stays in the map,
// concurrent callers share its detachAttempt, and a kernel failure preserves
// the handle for a later retry. The caller's cancellation is deliberately
// detached because teardown intent outlives the timed-out caller.
func (i *Importer) startCompensatingDetach(
	ctx context.Context, id domain.PortID, h *portHandle, attempt *detachAttempt,
) {
	detached := context.WithoutCancel(ctx)

	go func() {
		err := i.detachHandle(detached, id, h)
		h.finishDetachAttempt(attempt, err)
		i.wg.Done()
	}()
}

// resolveShutdownTimeout maps the user-supplied AttachOptions.ShutdownTimeout
// to its effective value: zero picks up the importer-lifecycle OpenSpec default; any other
// value (including negative) is passed through so callers can disable
// the bound by setting a negative value.
func resolveShutdownTimeout(t time.Duration) time.Duration {
	if t == 0 {
		return defaultShutdownTimeout
	}

	return t
}

// emptyEventSeq is the event-only iter used when there is nothing to iterate.
// Exporter.WatchSessions also uses it for its post-shutdown fast path.
func emptyEventSeq(_ func(domain.Event) bool) {}

// emptyEventErrorSeq is returned by WatchWithErrors after Importer.Close. It
// terminates immediately without fabricating either an event or an error.
func emptyEventErrorSeq(_ func(domain.Event, error) bool) {}
