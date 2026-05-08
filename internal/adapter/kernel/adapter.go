// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"io/fs"
	"log/slog"
	"sync"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// commonAdapter holds the shared state injected into every role
// adapter. Role adapters (ImporterAdapter, ExporterAdapter,
// EventsAdapter) embed commonAdapter so their methods can reach the
// injected fs.FS, WriteFunc, NetlinkDialer, logger, and clock without
// duplicating option plumbing.
//
// topoCache memoises the full discoverTopology result (BusMap
// inclusive) for BusMap consumers; statusTopoCache memoises the
// lighter discoverStatusTopology for the status-reading path so a
// transient BusMap shortfall does not hard-fail ListPorts /
// findFreePort. Both caches are heap-allocated (pointer embed) so that
// embedding commonAdapter by value in role-adapter structs remains
// safe under vet's copylocks check — a sync.Once inside a value-copied
// struct would fail vet and silently duplicate the memoised state.
type commonAdapter struct {
	fs              fs.FS
	write           WriteFunc
	nlDial          NetlinkDialer
	logger          *slog.Logger
	clock           app.Clock
	topoCache       *topologyCache
	statusTopoCache *statusTopologyCache
}

// topologyCache memoises a successful discoverTopology result and
// retries on every call after a transient failure — errors are never
// cached. A long-lived daemon that survives a vhci_hcd module reload
// must recover automatically; a sync.Once that memoised the first
// error would wedge the adapter forever. It is kept behind a pointer
// so copies of commonAdapter share the same underlying cache and vet's
// copylocks check never trips on commonAdapter values.
type topologyCache struct {
	mu   sync.Mutex
	topo Topology
	ok   bool
}

// statusTopologyCache memoises a successful discoverStatusTopology
// result with the same retry-on-error semantics as topologyCache.
// Separate from topologyCache so the status-reading path does not pay
// the BusMap walk (or its failure) that full-Topology consumers need.
type statusTopologyCache struct {
	mu   sync.Mutex
	topo StatusTopology
	ok   bool
}

// ImporterAdapter satisfies app.ImporterKernel. It operates against the
// vhci_hcd + usbip_core modules.
//
// attachMu serializes the findFreePort → sysfs-write critical section
// of AttachRemote per v1 contract §3.4 "Attach vs Attach race: first acquire
// wins; loser gets ErrNoFreePort". Without this lock, two concurrent
// AttachRemote callers could both observe the same free port in the
// status table and race on the sysfs attach write.
type ImporterAdapter struct {
	commonAdapter

	attachMu sync.Mutex
}

// ExporterAdapter satisfies app.ExporterKernel. It operates against the
// usbip_host + usbip_core modules.
//
// busidLocks serialises Bind/Unbind per busid so concurrent callers
// against the same device cannot race on match_busid: without this a
// loser's rollback (match_busid del) can erase the winner's just-
// added entry, leaving the kernel in a half-bound state.
type ExporterAdapter struct {
	commonAdapter

	busidLocks sync.Map // domain.BusID -> *sync.Mutex
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

// lockBusID returns a per-busid sync.Mutex, creating it on first use.
// Mutex is permanent — the entry stays in the map for the lifetime of
// the adapter; the memory cost is one mutex per ever-bound busid which
// is bounded by the number of physical USB ports the host has seen.
func (a *ExporterAdapter) lockBusID(busID domain.BusID) *sync.Mutex {
	v, _ := a.busidLocks.LoadOrStore(busID, &sync.Mutex{})

	mu, _ := v.(*sync.Mutex)

	return mu
}

// NewEventsAdapter constructs an EventsAdapter with the same defaults
// as NewImporterAdapter. dispMu is allocated eagerly here — lazy
// initialisation in Subscribe would race under concurrent first-
// Subscribers, letting two callers lock different mutexes and nlDial
// twice (leaking one dispatcher + netlink socket).
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
		fs:              osDirFS(),
		write:           defaultWriteFunc(),
		nlDial:          defaultNetlinkDialer(),
		logger:          noopLogger(),
		clock:           app.RealClock{},
		topoCache:       &topologyCache{},
		statusTopoCache: &statusTopologyCache{},
	}

	for _, opt := range opts {
		opt(&c)
	}

	return c
}
