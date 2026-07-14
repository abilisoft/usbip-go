// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"context"
	"io/fs"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// DiscoverTopologyForTest exposes the unexported discoverTopology so
// topology_test.go can drive a MapFS-backed discovery directly without
// constructing an adapter. The production code keeps discoverTopology
// internal so callers obtain operation-local snapshots through adapter paths.
func DiscoverTopologyForTest(fsys fs.FS) (Topology, error) {
	return discoverTopology(fsys)
}

// LoadTopologyForTest exposes the ImporterAdapter's fresh loadTopology method
// so tests can model module changes between consecutive operations.
func LoadTopologyForTest(a *ImporterAdapter) (Topology, error) {
	return a.loadTopology()
}

// CommonExport mirrors commonAdapter for white-box tests. Keeping a
// separate exported shape (rather than exposing commonAdapter directly)
// lets the tests assert field identity without widening the production
// API surface.
type CommonExport struct {
	FS          fs.FS
	Write       WriteFunc
	NetlinkDial NetlinkDialer
	Logger      *slog.Logger
	Clock       app.Clock
}

// ExportCommonFromImporter reveals the injected common state inside an
// ImporterAdapter for test assertions.
func ExportCommonFromImporter(a *ImporterAdapter) CommonExport {
	return commonSnapshot(a.commonAdapter)
}

// ExportCommonFromExporter reveals the injected common state inside an
// ExporterAdapter for test assertions.
func ExportCommonFromExporter(a *ExporterAdapter) CommonExport {
	return commonSnapshot(a.commonAdapter)
}

// ExportCommonFromEvents reveals the injected common state inside an
// EventsAdapter for test assertions.
func ExportCommonFromEvents(a *EventsAdapter) CommonExport {
	return commonSnapshot(a.commonAdapter)
}

func commonSnapshot(c commonAdapter) CommonExport {
	return CommonExport{
		FS:          c.fs,
		Write:       c.write,
		NetlinkDial: c.nlDial,
		Logger:      c.logger,
		Clock:       c.clock,
	}
}

// FindFreePortForTest exposes the internal findFreePort for unit
// tests. Role adapters share the algorithm via commonAdapter so the
// ImporterAdapter parameter is arbitrary — either role would produce
// identical results.
func FindFreePortForTest(a *ImporterAdapter, speed domain.Speed) (domain.PortID, error) {
	return a.findFreePort(speed)
}

// ErrPortOutOfRangeForTest exposes the adapter-local errPortOutOfRange
// sentinel for white-box tests. The production symbol stays unexported
// because the flat-port concept is VHCI-specific and has no place on
// pkg/domain or pkg/usbip — public callers see only a wrapped
// fmt.Errorf whose message carries port + nports context.
var ErrPortOutOfRangeForTest = errPortOutOfRange

// FormatAttachPayloadForTest exposes the unexported formatAttachPayload
// so tests can pin the exact byte-for-byte shape the adapter writes to
// the vhci_hcd attach sysfs node. The payload is the single source of
// truth for interop with vhci_sysfs.c::attach_store(); a lock-in test
// consuming this helper guards against silent reordering or format
// drift that a compile-only type check would miss.
func FormatAttachPayloadForTest(
	portID domain.PortID, fd uintptr, devID domain.DeviceID, speed domain.Speed,
) string {
	return formatAttachPayload(portID, fd, devID, speed)
}

// FormatDetachPayloadForTest exposes the unexported formatDetachPayload
// so tests can pin the exact byte-for-byte shape the adapter writes to
// the vhci_hcd detach sysfs node. Parallel to
// FormatAttachPayloadForTest: a lock-in test consuming this helper
// catches any silent drift (newline added, hex, leading zeros, sign
// prefix) in the decimal-integer rendering vhci_sysfs.c::detach_store
// consumes via kstrtoint(buf, 10, &port).
func FormatDetachPayloadForTest(portID domain.PortID) string {
	return formatDetachPayload(portID)
}

// PortMutationLockHeldForTest reports whether an AttachRemote or DetachPort
// operation currently owns the adapter's shared VHCI mutation boundary. It is
// intentionally observation-only: tests wait until the injected sysfs writer
// is running, then use TryLock to prove both public mutation paths hold the
// same adapter mutex across their writes.
func PortMutationLockHeldForTest(a *ImporterAdapter) bool {
	if a.portMutationMu.TryLock() {
		a.portMutationMu.Unlock()

		return false
	}

	return true
}

// AttachAtPortForTest exposes the unexported attachAtPort so tests
// can drive the post-selection half of AttachRemote with an explicit
// flat port — the synthetic "a bad port somehow reached attach"
// scenario the attach bounds check guards against. Production
// AttachRemote always routes through findFreePort first; this helper
// bypasses that step so the bounds-check contract can be pinned
// without depending on findFreePort emitting an out-of-range value
// (which parseStatusFile already prevents upstream).
//
// The helper threads context + conn + spec through the same code
// path the production method uses, so the write spy, fd extraction
// and conn-close lifecycle all match AttachRemote's guarantees.
func AttachAtPortForTest(
	ctx context.Context,
	a *ImporterAdapter,
	conn net.Conn,
	port domain.PortID,
	spec app.RemoteDeviceSpec,
) (domain.PortID, error) {
	return a.attachAtPort(ctx, conn, port, spec)
}

// ParseStatusFileForTest exposes the unexported parseStatusFile so
// ports-level tests can pin contract edges (e.g. defensive guards
// against vhciPorts=0) without synthesising a full adapter + sysfs
// layout. The adapter parameter supplies the logger and fs.FS that
// parseStatusFile threads through its Warn path.
func ParseStatusFileForTest(
	a *ImporterAdapter, body, source string, controllerIdx, vhciPorts uint32,
) ([]ParsedPortForTest, error) {
	rows, err := a.parseStatusFile(body, source, controllerIdx, vhciPorts)
	if err != nil {
		return nil, err
	}

	out := make([]ParsedPortForTest, 0, len(rows))
	for _, r := range rows {
		out = append(out, ParsedPortForTest{
			Hub:    r.hub,
			Port:   r.port,
			Status: r.status,
			Speed:  r.speed,
			DevID:  r.devID,
			BusID:  r.busID,
		})
	}

	return out, nil
}

// ParsedPortForTest mirrors parsedPort's fields through exported
// names. The production type stays unexported so callers cannot depend
// on its representation; tests consume this shape for assertions.
type ParsedPortForTest struct {
	Hub    string
	Port   domain.PortID
	Status domain.Status
	Speed  domain.Speed
	DevID  domain.DeviceID
	BusID  domain.BusID
}

// VHCIEventMapperForTest is the test-side façade over the internal
// vhciEventMapper. The inner mapper is held through a pointer so loader and
// usbip-host state synchronization persists across MapEventForTest calls.
type VHCIEventMapperForTest struct {
	inner *vhciEventMapper
}

// NewVHCIEventMapperForTest constructs a mapper against the supplied
// topology snapshot. Mirrors the internal newVHCIEventMapper.
func NewVHCIEventMapperForTest(topo Topology) VHCIEventMapperForTest {
	inner := newVHCIEventMapperWithLoaderAndWait(func() (Topology, error) {
		return topo, nil
	}, func(time.Duration) {})

	return VHCIEventMapperForTest{inner: &inner}
}

// NewVHCIEventMapperWithLoaderForTest constructs a mapper whose
// topology is produced lazily by the supplied loader. Tests drive
// this to exercise the exporter-only contract the Task-3 BUG-1 fix
// installs:
//   - deferred-load: the loader fires only for VHCI-shaped events, never at
//     construction or for exporter-only events;
//   - graceful degradation: a loader that returns an error must not
//     break usbip_host-path event mapping.
func NewVHCIEventMapperWithLoaderForTest(loader func() (Topology, error)) VHCIEventMapperForTest {
	inner := newVHCIEventMapperWithLoaderAndWait(loader, func(time.Duration) {})

	return VHCIEventMapperForTest{inner: &inner}
}

// NewVHCIEventMapperWithLoaderAndWaitForTest constructs a lazy mapper with a
// deterministic retry wait supplied by the caller.
func NewVHCIEventMapperWithLoaderAndWaitForTest(
	loader func() (Topology, error),
	wait func(time.Duration),
) VHCIEventMapperForTest {
	inner := newVHCIEventMapperWithLoaderAndWait(loader, wait)

	return VHCIEventMapperForTest{inner: &inner}
}

// MapEventForTest calls the internal mapEvent and returns its
// (domain.Event, bool) pair verbatim.
func (m VHCIEventMapperForTest) MapEventForTest(fields map[string]string) (domain.Event, bool) {
	return m.inner.mapEvent(fields)
}

// DetachSerializationHarness exposes only the synchronization observations
// needed by the external regression test.
type DetachSerializationHarness struct {
	adapter    *EventsAdapter
	dispatcher *eventDispatcher
}

// NewDetachSerializationHarness builds a dispatcher with one subscriber.
func NewDetachSerializationHarness() *DetachSerializationHarness {
	dispatcher := &eventDispatcher{
		subscribers: map[int64]chan domain.Event{1: make(chan domain.Event)},
	}

	return &DetachSerializationHarness{
		adapter:    &EventsAdapter{dispMu: &sync.Mutex{}, disp: dispatcher},
		dispatcher: dispatcher,
	}
}

// LockAdapter holds the adapter lifecycle lock.
func (h *DetachSerializationHarness) LockAdapter() { h.adapter.dispMu.Lock() }

// UnlockAdapter releases the adapter lifecycle lock.
func (h *DetachSerializationHarness) UnlockAdapter() { h.adapter.dispMu.Unlock() }

// Remove runs the last-subscriber removal path.
func (h *DetachSerializationHarness) Remove() bool {
	return h.adapter.removeSubscriberAndDetach(h.dispatcher, 1)
}

// SubscriberCount returns the current subscriber count under its lock.
func (h *DetachSerializationHarness) SubscriberCount() int {
	h.dispatcher.mu.Lock()
	defer h.dispatcher.mu.Unlock()

	return len(h.dispatcher.subscribers)
}

// Detached reports whether the adapter dropped the dispatcher.
func (h *DetachSerializationHarness) Detached() bool {
	h.adapter.dispMu.Lock()
	defer h.adapter.dispMu.Unlock()

	return h.adapter.disp == nil
}
