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
// Handle-map state (per-port generation + cancel func) is added in
// Task 5.6 together with Detach/ListPorts; the scaffolding exposes
// only the construction + Close surface that downstream tasks build
// on.
type Importer struct {
	kernel    ImporterKernel
	events    KernelEvents
	transport Transport
	codec     ProtocolCodec
	clock     Clock
	logger    *slog.Logger

	mu     sync.RWMutex
	closed bool
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
	}
}

// Close marks the Importer closed; it is idempotent so `defer Close()`
// is safe in test teardown. Task 5.6 extends the body with the full
// handle-map teardown (cancel every watcher, wait via sync.WaitGroup);
// the scaffolding version just flips the closed flag.
func (i *Importer) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.closed = true

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

	return domain.Port{
		ID:       portID,
		Status:   domain.StatusUsed,
		Speed:    dev.Speed,
		DeviceID: devID,
		Remote:   endpoint,
		BusID:    busID,
	}, nil
}
