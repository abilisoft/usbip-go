//go:build linux

package kernel

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// errNotYetImplemented marks an interface method whose body is filled
// in by a later Task 4.x GREEN step. Only Subscribe still uses it;
// Task 4.10 replaces this placeholder with the real fan-out
// implementation.
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

// Subscribe is wired by Task 4.10. The placeholder ensures
// EventsAdapter satisfies app.KernelEvents at compile time even before
// the netlink fan-out lands.
func (a *EventsAdapter) Subscribe(_ context.Context) (<-chan domain.Event, func(), error) {
	_ = a

	return nil, nil, fmt.Errorf("EventsAdapter.Subscribe: %w", errNotYetImplemented)
}
