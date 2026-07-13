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
// Topology is deliberately not retained in this shared state. A long-lived
// process can survive vhci_hcd unload/reload, so each importer operation and
// relevant event must discover a fresh snapshot rather than reuse controller,
// port, or bus mappings from a prior module generation.
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
// portMutationMu serializes every VHCI attach/detach topology check and
// sysfs mutation. Attach-vs-attach needs it to make free-port discovery
// atomic with the write; attach-vs-detach uses the same adapter-local
// boundary so their topology reads and kernel mutations cannot overlap.
type ImporterAdapter struct {
	commonAdapter

	portMutationMu sync.Mutex
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
