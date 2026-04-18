//go:build linux

package kernel

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// errNotYetImplemented marks an interface method whose body is filled
// in by a later Task 4.x GREEN step. Each subsequent task replaces one
// of these in-place with the real implementation; the ordering is
// documented in the Phase 4 plan header. The placeholder returns a
// real error so callers who prematurely invoke a not-yet-wired method
// observe it loudly rather than hitting a nil-pointer panic.
var errNotYetImplemented = errors.New("kernel adapter method wired by later Phase 4 task")

// commonAdapter holds the shared state injected into every role
// adapter. Role adapters (ImporterAdapter, ExporterAdapter,
// EventsAdapter) embed commonAdapter so their methods can reach the
// injected fs.FS, WriteFunc, NetlinkDialer, logger, and clock without
// duplicating option plumbing.
type commonAdapter struct {
	fs     fs.FS
	write  WriteFunc
	nlDial NetlinkDialer
	logger *slog.Logger
	clock  app.Clock
}

// ImporterAdapter satisfies app.ImporterKernel. It operates against the
// vhci_hcd + usbip_core modules.
type ImporterAdapter struct {
	commonAdapter
}

// ExporterAdapter satisfies app.ExporterKernel. It operates against the
// usbip_host + usbip_core modules.
type ExporterAdapter struct {
	commonAdapter
}

// EventsAdapter satisfies app.KernelEvents. It opens and shares a
// single NETLINK_KOBJECT_UEVENT socket across subscribers via an
// internal fan-out.
type EventsAdapter struct {
	commonAdapter
}

// NewImporterAdapter constructs an ImporterAdapter with defaults
// (os.DirFS("/"), live sysfs WriteFunc, live netlink dialer, no-op
// logger, RealClock). Options override in declaration order.
func NewImporterAdapter(opts ...Option) (*ImporterAdapter, error) {
	c := newCommon(opts...)

	return &ImporterAdapter{commonAdapter: c}, nil
}

// NewExporterAdapter constructs an ExporterAdapter with the same
// defaults as NewImporterAdapter.
func NewExporterAdapter(opts ...Option) (*ExporterAdapter, error) {
	c := newCommon(opts...)

	return &ExporterAdapter{commonAdapter: c}, nil
}

// NewEventsAdapter constructs an EventsAdapter with the same defaults
// as NewImporterAdapter.
func NewEventsAdapter(opts ...Option) (*EventsAdapter, error) {
	c := newCommon(opts...)

	return &EventsAdapter{commonAdapter: c}, nil
}

// newCommon applies the default substrate then each option in order.
// Keeping the defaults here rather than inside each constructor
// guarantees the three role adapters remain indistinguishable in their
// baseline configuration.
func newCommon(opts ...Option) commonAdapter {
	c := commonAdapter{
		fs:     osDirFS(),
		write:  defaultWriteFunc(),
		nlDial: defaultNetlinkDialer(),
		logger: noopLogger(),
		clock:  app.RealClock{},
	}

	for _, opt := range opts {
		opt(&c)
	}

	return c
}

// ImporterAdapter method placeholders. AttachRemote and ListPorts are
// wired in attach.go and ports.go respectively. DetachPort is wired
// in attach.go by Task 4.8.

// DetachPort is wired by Task 4.8.
func (a *ImporterAdapter) DetachPort(_ context.Context, _ domain.PortID) error {
	_ = a

	return fmt.Errorf("ImporterAdapter.DetachPort: %w", errNotYetImplemented)
}

// ExporterAdapter method placeholders. Wired by Task 4.8
// (export/disconnect). ModulesAvailable for both role adapters lives
// in modules.go; Bind and Unbind live in bind.go.

// ExportOnConn is wired by Task 4.8.
func (a *ExporterAdapter) ExportOnConn(_ context.Context, _ net.Conn, _ domain.BusID) error {
	_ = a

	return fmt.Errorf("ExporterAdapter.ExportOnConn: %w", errNotYetImplemented)
}

// Disconnect is wired by Task 4.8.
func (a *ExporterAdapter) Disconnect(_ context.Context, _ domain.BusID) error {
	_ = a

	return fmt.Errorf("ExporterAdapter.Disconnect: %w", errNotYetImplemented)
}

// EventsAdapter method placeholder. Wired by Task 4.10.

// Subscribe is wired by Task 4.10.
func (a *EventsAdapter) Subscribe(_ context.Context) (<-chan domain.Event, func(), error) {
	_ = a

	return nil, nil, fmt.Errorf("EventsAdapter.Subscribe: %w", errNotYetImplemented)
}
