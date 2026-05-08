package app

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// Importer is the use-case service that imports remote USB devices via
// the vhci_hcd kernel surface. One Importer is sufficient for a whole
// process; construct via NewImporter and release via Close. The zero
// value is not usable — NewImporter initialises required state.
//
// The handle map tracks every successfully-attached port along with a
// monotonically-increasing generation counter and a per-handle
// CancelFunc. The generation counter implements the stale-event
// protection described in spec §5.5: a watcher reading an event must
// compare its generation against the current handle generation and
// drop the event if the numbers differ (the handle was detached and
// re-attached in between).
type Importer struct {
	kernel    ImporterKernel
	events    KernelEvents
	transport Transport
	codec     ProtocolCodec
	clock     Clock
	logger    *slog.Logger

	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once
	handles   map[domain.PortID]*portHandle
	nextGen   uint64
	wg        sync.WaitGroup
}

// portHandle is the per-port bookkeeping entry for an active import.
// The done channel is closed exactly once (guarded by cancelOnce) when
// Detach or Close fires; the Task 5.8 watcher selects on it to observe
// termination. generation increments on every successful Attach of the
// same PortID so watchers can detect a detach/re-attach sequence
// without missing the transition (spec §5.5).
//
// Using a channel + sync.Once instead of a context sidesteps the
// containedctx linter while preserving the same semantics: done is a
// broadcast signal, a watcher derives its own ctx at launch time and
// selects on ctx.Done() alongside done.
type portHandle struct {
	generation uint64
	done       chan struct{}
	cancelOnce sync.Once
	busID      domain.BusID
	remote     domain.RemoteEndpoint
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

	return &Importer{
		kernel:    cfg.kernel,
		events:    cfg.events,
		transport: cfg.transport,
		codec:     cfg.codec,
		clock:     cfg.clock,
		logger:    cfg.logger,
		handles:   make(map[domain.PortID]*portHandle),
	}
}

// Close cancels every registered handle's context, waits for any
// background goroutines to drain, and marks the Importer closed.
// Subsequent Close calls are no-ops via sync.Once. The wait group is
// currently empty (auto-reconnect goroutines land in Task 5.8) but the
// wait is wired now so the contract is stable across that addition.
func (i *Importer) Close() error {
	i.closeOnce.Do(func() {
		i.mu.Lock()

		i.closed = true

		handles := i.handles

		i.handles = nil

		i.mu.Unlock()

		// Cancel outside the write lock: cancel funcs may try to
		// re-enter the Importer (e.g. future reconnect watcher
		// acquiring the RLock to check closed) and we must not hold
		// the write lock while doing so.
		for _, h := range handles {
			h.cancel()
		}

		i.wg.Wait()
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
// AutoReconnect is stubbed in this batch: when set to true, Attach
// returns ErrAutoReconnectNotImplemented without any side effects. The
// watcher goroutine is wired in Task 5.8.
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

	if opts.AutoReconnect {
		return domain.Port{}, ErrAutoReconnectNotImplemented
	}

	endpoint = endpoint.NormalizePort()

	err := i.kernel.ModulesAvailable(ctx)
	if err != nil {
		return domain.Port{}, fmt.Errorf("vhci modules unavailable: %w", err)
	}

	return i.attachOverDialed(ctx, endpoint, busID)
}

// Detach tears down a previously-imported port by id. It cancels the
// handle's context BEFORE issuing the sysfs-backed detach per spec §5.5
// so any auto-reconnect watcher (Task 5.8) sees cancel ahead of the
// status transition and does not race a reattempt. When the kernel
// rejects the detach, the handle is left registered so callers can
// retry.
func (i *Importer) Detach(ctx context.Context, id domain.PortID) error {
	i.mu.Lock()

	if i.closed {
		i.mu.Unlock()

		return ErrImporterClosed
	}

	h, ok := i.handles[id]
	if !ok {
		i.mu.Unlock()

		return fmt.Errorf("detach port %d: %w", id, domain.ErrDeviceNotBound)
	}

	i.mu.Unlock()

	// Cancel first (spec §5.5) — no watcher today, but the ordering
	// contract is load-bearing for Task 5.8 and must not drift.
	h.cancel()

	err := i.kernel.DetachPort(ctx, id)
	if err != nil {
		// Preserve the handle so callers can retry; the cancelled
		// context is harmless — any future watcher starts fresh from
		// the next successful Attach which regenerates the handle.
		return fmt.Errorf("detach port %d: %w", id, err)
	}

	i.mu.Lock()
	delete(i.handles, id)
	i.mu.Unlock()

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

// attachOverDialed factors out the dial-through-handoff portion of
// Attach. Splitting it keeps Attach under the project's cyclomatic cap
// and isolates the fd-passing deferred cleanup per spec §5.4.
func (i *Importer) attachOverDialed(
	ctx context.Context,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
) (domain.Port, error) {
	conn, err := i.transport.Dial(ctx, endpoint)
	if err != nil {
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
		return domain.Port{}, fmt.Errorf("encode OP_REQ_IMPORT for %s: %w", busID, err)
	}

	dev, err := i.codec.DecodeOpRepImport(conn)
	if err != nil {
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
		return domain.Port{}, fmt.Errorf("attach %s on %s: %w", busID, endpoint.String(), err)
	}

	handedOff = true

	i.registerHandle(portID, busID, endpoint)

	return domain.Port{
		ID:       portID,
		Status:   domain.StatusUsed,
		Speed:    dev.Speed,
		DeviceID: devID,
		Remote:   endpoint,
		BusID:    busID,
	}, nil
}

// registerHandle records a successful attach in the handle map. If an
// entry already existed for this PortID (the kernel re-used the slot
// after a previous detach we didn't observe), its cancel func fires
// first so any in-flight consumer sees termination before the new
// generation appears.
func (i *Importer) registerHandle(id domain.PortID, busID domain.BusID, endpoint domain.RemoteEndpoint) {
	i.mu.Lock()
	defer i.mu.Unlock()

	if old, ok := i.handles[id]; ok {
		old.cancel()
	}

	i.nextGen++

	i.handles[id] = &portHandle{
		generation: i.nextGen,
		done:       make(chan struct{}),
		busID:      busID,
		remote:     endpoint,
	}
}
