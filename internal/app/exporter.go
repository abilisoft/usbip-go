package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// Exporter is the use-case service that exports local USB devices via
// the usbip_host kernel surface. One Exporter is sufficient for a whole
// daemon process; construct via NewExporter and release via Shutdown.
// The zero value is not usable — NewExporter initialises required state.
type Exporter struct {
	kernel    ExporterKernel
	events    KernelEvents
	transport Transport
	codec     ProtocolCodec
	clock     Clock
	logger    *slog.Logger
}

// NewExporter constructs an Exporter from functional options. Required
// dependencies missing from opts cause a panic because a missing
// dependency is a programming error, not a runtime condition worth
// propagating. An invalid CIDR in the allow-list is also a programming
// error and panics with a clear message.
func NewExporter(opts ...ExporterOption) *Exporter {
	cfg := exporterConfig{clock: RealClock{}, logger: slog.Default()}

	for _, opt := range opts {
		opt(&cfg)
	}

	requireExporterDeps(&cfg)

	if cfg.logger == nil {
		cfg.logger = slog.Default()
	}

	return &Exporter{
		kernel:    cfg.kernel,
		events:    cfg.events,
		transport: cfg.transport,
		codec:     cfg.codec,
		clock:     cfg.clock,
		logger:    cfg.logger,
	}
}

// requireExporterDeps panics when any of the mandatory option-supplied
// dependencies is nil. Split from NewExporter to keep cyclomatic
// complexity within the project cap.
func requireExporterDeps(cfg *exporterConfig) {
	if cfg.kernel == nil {
		panic("app.NewExporter: ExporterKernel is required (use WithExporterKernel)")
	}

	if cfg.events == nil {
		panic("app.NewExporter: KernelEvents is required (use WithExporterEvents)")
	}

	if cfg.transport == nil {
		panic("app.NewExporter: Transport is required (use WithExporterTransport)")
	}

	if cfg.codec == nil {
		panic("app.NewExporter: ProtocolCodec is required (use WithExporterCodec)")
	}
}

// Bind delegates to kernel.Bind per spec §5.3. Errors are wrapped with
// the busid so callers that bind many devices can identify which failed.
func (e *Exporter) Bind(ctx context.Context, busID domain.BusID) error {
	err := e.kernel.Bind(ctx, busID)
	if err != nil {
		return fmt.Errorf("bind %s: %w", busID, err)
	}

	return nil
}

// Unbind delegates to kernel.Unbind per spec §5.3. Errors are wrapped
// with the busid.
func (e *Exporter) Unbind(ctx context.Context, busID domain.BusID) error {
	err := e.kernel.Unbind(ctx, busID)
	if err != nil {
		return fmt.Errorf("unbind %s: %w", busID, err)
	}

	return nil
}

// ListAvailable forwards to kernel.ListLocalDevices per spec §5.3. The
// kernel adapter is the authoritative source; the Exporter does not
// maintain a cache of its own.
func (e *Exporter) ListAvailable(ctx context.Context) ([]domain.Device, error) {
	devs, err := e.kernel.ListLocalDevices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list local devices: %w", err)
	}

	return devs, nil
}
