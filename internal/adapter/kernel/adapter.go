//go:build linux

package kernel

import (
	"io/fs"
	"log/slog"
	"sync"

	"github.com/abilisoft/usbip-go/internal/app"
)

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
//
// attachMu serializes the findFreePort → sysfs-write critical section
// of AttachRemote per spec §3.4 "Attach vs Attach race: first acquire
// wins; loser gets ErrNoFreePort". Without this lock, two concurrent
// AttachRemote callers could both observe the same free port in the
// status table and race on the sysfs attach write.
type ImporterAdapter struct {
	commonAdapter

	attachMu sync.Mutex
}

// ExporterAdapter satisfies app.ExporterKernel. It operates against the
// usbip_host + usbip_core modules.
type ExporterAdapter struct {
	commonAdapter
}

// EventsAdapter satisfies app.KernelEvents. It opens and shares a
// single NETLINK_KOBJECT_UEVENT socket across subscribers via an
// internal fan-out. dispMu guards the first Subscribe that lazily
// opens the socket; disp is the live dispatcher or nil when no
// subscribers are active.
type EventsAdapter struct {
	commonAdapter

	dispMu *sync.Mutex
	disp   *eventDispatcher
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

