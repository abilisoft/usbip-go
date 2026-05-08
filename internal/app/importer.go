package app

import (
	"log/slog"
	"sync"
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
