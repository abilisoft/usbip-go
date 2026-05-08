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
//
// topoCache memoises the result of discoverTopology so downstream port
// arithmetic (Task 2+) pays the sysfs walk at most once per adapter
// instance. The cache is heap-allocated (pointer embed) so that
// embedding commonAdapter by value in role-adapter structs remains
// safe under vet's copylocks check — a sync.Once inside a value-copied
// struct would fail vet and silently duplicate the memoised state.
type commonAdapter struct {
	fs        fs.FS
	write     WriteFunc
	nlDial    NetlinkDialer
	logger    *slog.Logger
	clock     app.Clock
	topoCache *topologyCache
}

// topologyCache memoises a single discoverTopology result. It is kept
// behind a pointer so copies of commonAdapter share the same underlying
// cache and vet's copylocks check never trips on commonAdapter values.
type topologyCache struct {
	once sync.Once
	topo Topology
	err  error
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
// as NewImporterAdapter. dispMu is allocated eagerly here — lazy
// initialisation in Subscribe would race under concurrent first-
// Subscribers (RANK 4), letting two callers lock different mutexes
// and nlDial twice (leaking one dispatcher + netlink socket).
func NewEventsAdapter(opts ...Option) (*EventsAdapter, error) {
	c := newCommon(opts...)

	return &EventsAdapter{
		commonAdapter: c,
		dispMu:        &sync.Mutex{},
	}, nil
}

// newCommon applies the default substrate then each option in order.
// Keeping the defaults here rather than inside each constructor
// guarantees the three role adapters remain indistinguishable in their
// baseline configuration. The topology cache is heap-allocated here so
// every copy of the returned commonAdapter shares the same memoised
// snapshot.
func newCommon(opts ...Option) commonAdapter {
	c := commonAdapter{
		fs:        osDirFS(),
		write:     defaultWriteFunc(),
		nlDial:    defaultNetlinkDialer(),
		logger:    noopLogger(),
		clock:     app.RealClock{},
		topoCache: &topologyCache{},
	}

	for _, opt := range opts {
		opt(&c)
	}

	return c
}

