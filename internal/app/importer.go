package app

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// defaultShutdownTimeout bounds Detach and Close's wait on the reconnect
// watcher per spec §5.5 when AttachOptions.ShutdownTimeout is zero. A
// negative ShutdownTimeout disables the bound (wait indefinitely).
const defaultShutdownTimeout = 5 * time.Second

// Importer is the use-case service that imports remote USB devices via
// the vhci_hcd kernel surface. One Importer is sufficient for a whole
// process; construct via NewImporter and release via Close. The zero
// value is not usable — NewImporter initialises required state.
//
// The handle map tracks every successfully-attached port along with a
// per-handle cancel signal and a monotonically increasing generation.
// The reconnect watcher (spec §5.5) reads the generation to filter
// stale kernel events whose port id was replaced by a successful
// reattach.
type Importer struct {
	kernel    ImporterKernel
	events    KernelEvents
	transport Transport
	codec     ProtocolCodec
	clock     Clock
	logger    *slog.Logger
	metrics   *Metrics

	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
	handles   map[domain.PortID]*portHandle
	nextGen   uint64
	wg        sync.WaitGroup
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
// replaced by a successful reattach (spec §5.5).
//
// watcherDone is closed by the reconnect watcher goroutine when it
// exits. Non-AutoReconnect handles leave it nil; Detach and Close read
// it to synchronise with the watcher before issuing the kernel detach.
//
// shutdownTimeout bounds how long Detach and Close are willing to block
// on watcherDone before proceeding anyway. Carried on the handle (not
// on the Importer) because it is set per-Attach and must outlast the
// Attach call itself.
type portHandle struct {
	done            chan struct{}
	cancelOnce      sync.Once
	busID           domain.BusID
	remote          domain.RemoteEndpoint
	generation      uint64
	watcherDone     chan struct{}
	shutdownTimeout time.Duration
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

	if cfg.metrics == nil {
		cfg.metrics = MustNewMetrics(nil)
	}

	return &Importer{
		kernel:    cfg.kernel,
		events:    cfg.events,
		transport: cfg.transport,
		codec:     cfg.codec,
		clock:     cfg.clock,
		logger:    cfg.logger,
		metrics:   cfg.metrics,
		handles:   make(map[domain.PortID]*portHandle),
	}
}

// Close cancels every registered handle's context, waits for any
// background goroutines to drain, and marks the Importer closed.
// Subsequent Close calls are no-ops via sync.Once. The wait group is
// currently empty (auto-reconnect goroutines land in Task 5.8) but the
// wait is wired now so the contract is stable across that addition.
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
	})

	return nil
}

// ListRemote dials endpoint, requests the remote device list via
// OP_REQ_DEVLIST, and returns the decoded []domain.Device. The TCP
// connection is owned for the entire call: it is always closed before
// ListRemote returns (success or failure). OP_REP_DEVLIST does not
// involve fd-passing, so the spec §5.4 handoff contract does not apply
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

	endpoint = endpoint.NormalizePort()

	conn, err := i.transport.Dial(ctx, endpoint)
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

// Attach runs the full USB/IP import sequence per spec §5.2:
//
//  1. kernel.ModulesAvailable probes vhci_hcd + usbip_core.
//  2. transport.Dial establishes the TCP connection to endpoint.
//  3. codec.EncodeOpReqImport(conn, busID) writes the request.
//  4. codec.DecodeOpRepImport(conn) reads back the device body.
//  5. kernel.AttachRemote(ctx, conn, spec) hands the fd to the kernel.
//
// Step 5 is the fd-passing handoff defined in spec §5.4 item 4. Until
// AttachRemote returns success, Attach owns the conn and MUST close it
// on any error path. After success, the kernel owns the fd and Attach
// MUST NOT touch it — closing it there would tear down the just-opened
// vhci port. The local `handedOff` flag implements that split: the
// deferred cleanup is a no-op once handedOff flips to true.
//
// When AttachOptions.AutoReconnect is set, the successful-return path
// also spawns a reconnect watcher goroutine bound to the fresh handle
// (spec §5.5). The watcher is enrolled in i.wg so Close drains it.
func (i *Importer) Attach(
	ctx context.Context,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
	opts AttachOptions,
) (domain.Port, error) {
	i.mu.RLock()

	closed := i.closed

	i.mu.RUnlock()

	if closed {
		return domain.Port{}, ErrImporterClosed
	}

	endpoint = endpoint.NormalizePort()

	err := i.kernel.ModulesAvailable(ctx)
	if err != nil {
		i.metrics.ImporterAttached(AttachOutcomeKernelError)

		return domain.Port{}, fmt.Errorf("vhci modules unavailable: %w", err)
	}

	// attachOverDialed emits the outcome-specific metric itself at each
	// failure branch (dial/kernel/...) so the classification lives with
	// the error origin. On success, it records OK and updates the ports
	// gauge before returning. Keeping the metric emission adjacent to
	// the state transition means a future new error path cannot slip
	// past unclassified.
	return i.attachOverDialed(ctx, endpoint, busID, opts)
}

// Detach tears down a previously-imported port by id. It cancels the
// handle's context BEFORE issuing the sysfs-backed detach per spec §5.5
// so any auto-reconnect watcher sees cancel ahead of the status
// transition and does not race a reattempt, and blocks on the watcher
// goroutine's done channel before touching the kernel so an in-flight
// reconnect attempt cannot overlap with the sysfs write. When the
// kernel rejects the detach, the handle is left registered so callers
// can retry.
func (i *Importer) Detach(ctx context.Context, id domain.PortID) error {
	i.mu.Lock()

	if i.closed {
		i.mu.Unlock()

		return ErrImporterClosed
	}

	h, ok := i.handles[id]
	if !ok {
		i.mu.Unlock()

		i.metrics.ImporterDetached(DetachOutcomeNotFound)

		return fmt.Errorf("detach port %d: %w", id, domain.ErrDeviceNotBound)
	}

	// Enrol the kernel-side detach in the waitgroup BEFORE releasing
	// the lock. Close acquires the lock, flips closed=true, then waits
	// on i.wg — so incrementing here guarantees Close observes the
	// in-flight detach and blocks until it drains, closing the window
	// where Close could return while sysfs writes are still in-flight.
	i.wg.Add(1)
	defer i.wg.Done()

	i.mu.Unlock()

	// Cancel first (spec §5.5) so any reconnect watcher observes
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
		i.metrics.ImporterDetached(DetachOutcomeError)

		// Preserve the handle so callers can retry; the cancelled
		// context is harmless — any future watcher starts fresh from
		// the next successful Attach which regenerates the handle.
		return fmt.Errorf("detach port %d: %w", id, err)
	}

	i.mu.Lock()
	delete(i.handles, id)
	i.mu.Unlock()

	i.metrics.ImporterDetached(DetachOutcomeOK)
	i.updateImporterPortsGauge()

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
func (i *Importer) Watch(ctx context.Context) iter.Seq[domain.Event] {
	i.mu.RLock()

	closed := i.closed

	i.mu.RUnlock()

	if closed {
		return emptyEventSeq
	}

	ch, cancel, err := i.events.Subscribe(ctx)
	if err != nil {
		i.logger.Warn("watch subscribe failed", slog.Any("err", err))

		return emptyEventSeq
	}

	return newEventSeq(ctx, ch, cancel)
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

// classifyDecodeImportErr maps a DecodeOpRepImport failure onto the
// closest §11.5.5 AttachOutcome label. A non-zero OP_REP_IMPORT status
// surfaces as domain.ErrDeviceNotFound (RANK 5) — a domain-level
// rejection, not a wire framing fault; any other decode error is a
// genuine protocol mismatch. The closed-set outcome label for "peer
// rejected the import" stays kernel_error because the spec §11.5.5
// outcome enum does not yet split "rejected" from "kernel_error"; the
// important fix is that errors.Is(err, domain.ErrDeviceNotFound) is
// true on the returned Attach error so callers can distinguish.
func classifyDecodeImportErr(err error) AttachOutcome {
	if errors.Is(err, domain.ErrDeviceNotFound) {
		return AttachOutcomeKernelError
	}

	return AttachOutcomeProtocolMismatch
}

// attachOverDialed factors out the dial-through-handoff portion of
// Attach. Splitting it keeps Attach under the project's cyclomatic cap
// and isolates the fd-passing deferred cleanup per spec §5.4. opts is
// forwarded unchanged so registerHandle can hand the resulting handle
// to a reconnect watcher when AutoReconnect is enabled.
func (i *Importer) attachOverDialed(
	ctx context.Context,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
	opts AttachOptions,
) (domain.Port, error) {
	conn, err := i.transport.Dial(ctx, endpoint)
	if err != nil {
		i.metrics.ImporterAttached(AttachOutcomeDialFailed)

		return domain.Port{}, fmt.Errorf("dial %s: %w", endpoint.String(), err)
	}

	// Per spec §5.4 item 4: Attach owns the fd until AttachRemote
	// succeeds. The deferred close below runs on every return; the
	// handedOff flag suppresses it exactly once, right after the
	// kernel takes ownership.
	handedOff := false

	defer func() {
		if !handedOff {
			closeConnLogging(conn, i.logger)
		}
	}()

	err = i.codec.EncodeOpReqImport(conn, busID)
	if err != nil {
		i.metrics.ImporterAttached(AttachOutcomeProtocolMismatch)

		return domain.Port{}, fmt.Errorf("encode OP_REQ_IMPORT for %s: %w", busID, err)
	}

	dev, err := i.codec.DecodeOpRepImport(conn)
	if err != nil {
		i.metrics.ImporterAttached(classifyDecodeImportErr(err))

		return domain.Port{}, fmt.Errorf("decode OP_REP_IMPORT from %s: %w", endpoint.String(), err)
	}

	devID := domain.DeviceID((uint32(dev.BusNum) << deviceIDBusShift) | uint32(dev.DevNum))

	spec := RemoteDeviceSpec{
		Device: dev,
		DevID:  devID,
		Speed:  dev.Speed,
		Remote: endpoint,
	}

	portID, err := i.kernel.AttachRemote(ctx, conn, spec)
	if err != nil {
		i.metrics.ImporterAttached(AttachOutcomeKernelError)
		i.logger.Warn("attach failed",
			slog.Any("busid", busID),
			slog.String("remote", endpoint.String()),
			slog.Any("err", err),
		)

		return domain.Port{}, fmt.Errorf("attach %s on %s: %w", busID, endpoint.String(), err)
	}

	handedOff = true

	return i.finishAttach(ctx, portID, busID, endpoint, dev, devID, opts)
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
	h, err := i.registerHandle(portID, busID, endpoint, resolveShutdownTimeout(opts.ShutdownTimeout))
	if err != nil {
		i.metrics.ImporterAttached(AttachOutcomeKernelError)

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

		return domain.Port{}, err
	}

	i.metrics.ImporterAttached(AttachOutcomeOK)
	i.updateImporterPortsGauge()

	if opts.AutoReconnect {
		i.spawnReconnectWatcher(ctx, h, portID, endpoint, busID, opts)
	}

	return domain.Port{
		ID:       portID,
		Status:   domain.StatusUsed,
		Speed:    dev.Speed,
		DeviceID: devID,
		Remote:   endpoint,
		BusID:    busID,
	}, nil
}

// updateImporterPortsGauge snapshots the handle-map size under the
// Importer RLock and pushes it to usbip_importer_ports_active. Called
// on every transition that changes the live-port count (successful
// Attach, successful Detach, Close sweep).
func (i *Importer) updateImporterPortsGauge() {
	i.mu.RLock()

	n := len(i.handles)

	i.mu.RUnlock()

	i.metrics.ImporterPortsActive(n)
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

// newEventSeq returns an iter.Seq that drains ch until ctx is cancelled
// or ch closes or yield returns false. The unsubscribe cancel is always
// called on exit so the KernelEvents fan-out releases the buffered
// channel promptly.
func newEventSeq(ctx context.Context, ch <-chan domain.Event, cancel func()) iter.Seq[domain.Event] {
	return func(yield func(domain.Event) bool) {
		defer cancel()

		for drainOne(ctx, ch, yield) {
			// loop until drainOne reports termination.
		}
	}
}

// drainOne performs a single select over ctx and ch, delivering one
// event via yield if one is available. Returns true to continue the
// loop, false to terminate. Keeping the select in its own function
// takes the cognitive load off Watch's caller-visible body.
func drainOne(ctx context.Context, ch <-chan domain.Event, yield func(domain.Event) bool) bool {
	select {
	case <-ctx.Done():
		return false
	case ev, ok := <-ch:
		if !ok {
			return false
		}

		return yield(ev)
	}
}
