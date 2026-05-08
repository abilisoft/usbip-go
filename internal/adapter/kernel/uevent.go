// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// netlinkUeventBufSize is the socket receive buffer size used by the
// kernel-provided netlink listener. 32 KiB matches the kernel-default
// uevent send buffer; larger buffers are silently clamped.
const netlinkUeventBufSize = 32 * 1024

// netlinkUdevGroup is the multicast group the udev daemon subscribes
// to. Joining this group lets us see the same uevent stream udev does.
const netlinkUdevGroup = 1

// subscriberChanBuffer bounds each subscriber's queued events. Slow
// consumers that fill this buffer drop subsequent events (logged).
const subscriberChanBuffer = 32

// eventDispatcher owns the single netlink socket and the fan-out list
// of subscriber channels. EventsAdapter lazily constructs one on the
// first Subscribe call and tears it down on the last Unsubscribe.
//
// Shutdown uses a stop channel (closed by cancel()) instead of a
// stored context so the struct is not `containedctx`-flagged and the
// dispatcher's lifetime is unambiguously independent of any caller's
// ctx. done closes when run() actually exits, so tearDown can block
// on orderly teardown.
//
// mapper is the topology-aware translator for vhci_hcd devpaths: it
// resolves each uevent's (usbBus, rootPort) pair into the kernel's
// flat Port.ID via the cached BusMap. EventsAdapter populates it at
// dispatcher construction time so the run loop sees a consistent
// snapshot for its entire lifetime.
type eventDispatcher struct {
	mu          sync.Mutex
	sock        NetlinkSocket
	subscribers map[int64]chan domain.Event
	nextID      int64
	stop        chan struct{}
	stopOnce    sync.Once
	done        chan struct{}
	logger      *slogRef
	mapper      vhciEventMapper
}

// slogRef is a tiny alias around *slog.Logger so a nil logger cannot
// crash the fan-out. EventsAdapter injects the concrete reference.
type slogRef = struct {
	warn func(msg string, args ...any)
}

// Subscribe returns a buffered event channel fed by the internal
// netlink listener. The first Subscribe opens the socket and starts
// the dispatcher run-loop; subsequent Subscribes join the existing
// fan-out. Each caller receives its own cancel func; the dispatcher
// lives until the LAST subscriber unsubscribes (v1 contract §5.1), not the
// first — the dispatcher carries its own context independent of any
// subscriber's.
//
// Registration ordering: the subscriber is added to the fan-out map
// BEFORE the run-loop goroutine is kicked off on the first call, so
// there is no window in which events could be broadcast to an empty
// subscriber set.
func (a *EventsAdapter) Subscribe(ctx context.Context) (<-chan domain.Event, func(), error) {
	// NewEventsAdapter eagerly allocates dispMu so concurrent first-
	// Subscribers cannot race the pointer write. Lock the shared
	// mutex directly.
	a.dispMu.Lock()
	defer a.dispMu.Unlock()

	dispatcher, firstSubscribe, err := a.ensureDispatcher()
	if err != nil {
		return nil, nil, err
	}

	ch := make(chan domain.Event, subscriberChanBuffer)
	id := dispatcher.addSubscriber(ch)

	// Start the run-loop ONLY after the first subscriber is registered.
	// Any events the socket emits immediately after this point have at
	// least one consumer in the fan-out map.
	if firstSubscribe {
		go dispatcher.run()

		// Cleanup goroutine: fires whenever run() exits — intentional
		// teardown or fatal netlink error. On intentional teardown,
		// a.disp is already nil and the socket is already closed, so
		// the guarded operations below are no-ops. On unexpected exit,
		// we close all subscriber channels (callers see end-of-events)
		// and clear the stale disp pointer so the next Subscribe dials
		// a fresh connection.
		go func() {
			<-dispatcher.done

			dispatcher.closeAllSubscribers()

			a.dispMu.Lock()
			if a.disp == dispatcher {
				a.disp = nil
				_ = dispatcher.sock.Close()
			}
			a.dispMu.Unlock()
		}()
	}

	// unsubSig lets the ctx-watcher goroutine below exit when the
	// caller releases the subscription via unsub(). Without it, an
	// explicit-unsub path strands the watcher on ctx.Done() for the
	// lifetime of the caller's (possibly very long-lived) ctx.
	unsubSig := make(chan struct{})
	unsub := a.buildUnsubscribe(dispatcher, id, unsubSig)

	// Honour the caller's ctx — cancel auto-unsubscribes this
	// subscriber only. The dispatcher keeps running as long as any
	// other subscriber is attached. Exits on whichever signal fires
	// first: ctx cancellation or explicit unsub.
	go func() {
		select {
		case <-ctx.Done():
			unsub()
		case <-unsubSig:
		}
	}()

	return ch, unsub, nil
}

// ensureDispatcher opens the netlink socket on first call and reuses
// the existing dispatcher on subsequent calls. The dispatcher carries
// its OWN context (independent of any subscriber), so no single
// subscriber can shut the listener down for others; tearDownDispatcher
// is the only caller of the dispatcher's cancel.
//
// Returns firstSubscribe=true on the call that constructed the
// dispatcher; the caller uses this signal to start the run goroutine
// AFTER registering the first subscriber.
//
// The VHCI topology is NOT loaded here — the mapper receives a loader
// that fires lazily on the first VHCI-shaped event. Exporter-only
// deployments (hosts running usbip_host without vhci_hcd) therefore
// succeed at Subscribe and continue delivering DeviceBoundEvent /
// DeviceUnboundEvent from the usbip_host subsystem; only VHCI-shaped
// events would fail to map, and those never arrive on exporter-only
// hosts. A sysfs error at first VHCI-event time degrades only the
// VHCI branch — the usbip_host stream is unaffected, and the adapter-
// level topology cache (see topology.go: loadTopology) retries on
// every call so the mapper can pick up a successful load once the
// vhci_hcd module is (re)loaded.
func (a *EventsAdapter) ensureDispatcher() (*eventDispatcher, bool, error) {
	if a.disp != nil {
		return a.disp, false, nil
	}

	sock, err := a.nlDial()
	if err != nil {
		return nil, false, fmt.Errorf("dial netlink: %w", err)
	}

	mapper := newVHCIEventMapperWithLoader(a.loadTopology)

	d := newDispatcher(sock, a.logger, mapper)

	a.disp = d

	return d, true, nil
}

// newDispatcher constructs an eventDispatcher whose lifetime is
// controlled by a stop channel (closed by cancel()). No subscriber
// can shorten the dispatcher's life.
func newDispatcher(sock NetlinkSocket, logger *slog.Logger, mapper vhciEventMapper) *eventDispatcher {
	return &eventDispatcher{
		sock:        sock,
		subscribers: make(map[int64]chan domain.Event),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		logger: &slogRef{
			warn: func(msg string, args ...any) { logger.Warn(msg, args...) },
		},
		mapper: mapper,
	}
}

// cancel closes the stop channel (idempotent via stopOnce). run()
// selects on d.stop and returns when it is closed.
func (d *eventDispatcher) cancel() {
	d.stopOnce.Do(func() { close(d.stop) })
}

// buildUnsubscribe returns a func that removes a subscriber and, if
// it was the last one, shuts the dispatcher down. Closing unsubSig on
// the first call releases Subscribe's per-subscription ctx-watcher
// goroutine so it does not leak for the lifetime of the caller's ctx
// when unsub is invoked directly.
func (a *EventsAdapter) buildUnsubscribe(d *eventDispatcher, id int64, unsubSig chan struct{}) func() {
	var once sync.Once

	return func() {
		once.Do(func() {
			close(unsubSig)

			empty := d.removeSubscriber(id)
			if !empty {
				return
			}

			a.tearDownDispatcher(d)
		})
	}
}

// tearDownDispatcher cancels the run loop and closes the socket.
func (a *EventsAdapter) tearDownDispatcher(d *eventDispatcher) {
	a.dispMu.Lock()

	if a.disp == d {
		a.disp = nil
	}

	a.dispMu.Unlock()

	d.cancel()

	_ = d.sock.Close()

	<-d.done
}

// addSubscriber registers ch and returns its id.
func (d *eventDispatcher) addSubscriber(ch chan domain.Event) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.nextID++

	id := d.nextID

	d.subscribers[id] = ch

	return id
}

// removeSubscriber drops id and closes its channel. Returns true when
// the last subscriber was removed.
func (d *eventDispatcher) removeSubscriber(id int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	ch, ok := d.subscribers[id]
	if !ok {
		return false
	}

	delete(d.subscribers, id)
	close(ch)

	return len(d.subscribers) == 0
}

// closeAllSubscribers closes every remaining subscriber channel under
// the mutex. Called by the cleanup goroutine when run() exits
// unexpectedly so blocked readers see end-of-events and can respond.
// Concurrent removeSubscriber calls are safe: the channel is deleted
// first so a subsequent call finds ok=false and skips the close.
func (d *eventDispatcher) closeAllSubscribers() {
	d.mu.Lock()
	defer d.mu.Unlock()

	for _, ch := range d.subscribers {
		close(ch)
	}

	clear(d.subscribers)
}

// run is the dispatcher's read loop. Reads one payload, parses it,
// and broadcasts any resulting domain.Event to every subscriber. Slow
// subscribers drop events via the buffered channel's overflow path.
// The deferred d.cancel() ensures the stop channel is closed on every
// exit path so parallel teardown callers wake up consistently.
func (d *eventDispatcher) run() {
	defer close(d.done)
	defer d.cancel()

	for {
		select {
		case <-d.stop:
			return
		default:
		}

		payload, keepRunning := d.receiveOne()
		if !keepRunning {
			return
		}

		if payload == nil {
			continue
		}

		fields := parseUeventFields(payload)

		ev, ok := d.mapper.mapEvent(fields)
		if !ok {
			continue
		}

		d.broadcast(ev)
	}
}

// handleReceiveErr examines the receive error. io.EOF or a closed
// socket is a clean termination; other errors surface via the logger.
func (d *eventDispatcher) handleReceiveErr(err error) {
	if errors.Is(err, io.EOF) {
		return
	}

	d.logger.warn("netlink receive error", "err", err)
}

// receiveOne pulls one uevent payload from the netlink socket. It
// returns (payload, true) for a usable payload, (nil, true) for the
// benign SO_RCVTIMEO wake that asks run()'s loop to re-check the stop
// channel, and (nil, false) when the socket errored beyond recovery
// (EOF, closed fd, etc.) — the caller must exit the run loop in that
// last case. Extracting the branching keeps run()'s cognitive
// complexity under the linter cap.
func (d *eventDispatcher) receiveOne() ([]byte, bool) {
	payload, err := d.sock.Receive()
	if err == nil {
		return payload, true
	}

	if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EWOULDBLOCK) {
		// SO_RCVTIMEO woke us up. No event was lost — the timeout
		// fires only when the socket queue is empty.
		return nil, true
	}

	d.handleReceiveErr(err)

	return nil, false
}

// broadcast fan-outs ev to every subscriber. A full buffer triggers a
// drop + log rather than blocking the reader.
func (d *eventDispatcher) broadcast(ev domain.Event) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for id, ch := range d.subscribers {
		select {
		case ch <- ev:
		default:
			d.logger.warn("dropped uevent — subscriber buffer full", "subscriber_id", id)
		}
	}
}

// topologyLoader is the deferred-fetch contract the mapper uses to
// resolve the VHCI topology it needs for flat-Port.ID translation.
// The mapper invokes it lazily on the first VHCI-shaped event so
// exporter-only deployments (no vhci_hcd module loaded) never pay the
// topology read and never surface a load error on Subscribe; the
// usbip_host path bypasses the loader entirely.
type topologyLoader func() (Topology, error)

// vhciEventMapper is the stateful translator that the dispatcher uses
// to turn each parsed uevent fields map into a domain.Event.
//
// It is constructed with a topology loader rather than a pre-resolved
// Topology so exporter-only deployments — which only need usbip_host
// events and never touch vhci_hcd — can still start the dispatcher
// without hard-failing on a missing VHCI module. The loader fires
// lazily on the first VHCI-shaped event. A loader failure is NOT
// memoised — the next VHCI event retries, allowing recovery after a
// transient sysfs error or vhci_hcd module reload. A failure degrades
// only the VHCI branch: the event is dropped with ok=false, while
// usbip_host events continue unaffected.
//
// mu is held through a pointer so copies of vhciEventMapper share the
// same lock and vet's copylocks check never trips — the dispatcher
// currently runs mapEvent from a single goroutine, but keeping the
// guard self-synchronising costs nothing and future-proofs fan-in of
// the mapper if mapEvent is ever invoked from multiple readers.
type vhciEventMapper struct {
	load   topologyLoader
	mu     *sync.Mutex
	topo   Topology
	loaded bool
}

// newVHCIEventMapper returns a mapper pinned to a fully-resolved
// topology snapshot. Construction wraps the Topology in an always-
// succeeding loader so the remainder of the mapper's machinery
// (sync.Once, lazy resolution) is uniform across the two construction
// paths.
func newVHCIEventMapper(topo Topology) vhciEventMapper {
	return newVHCIEventMapperWithLoader(func() (Topology, error) {
		return topo, nil
	})
}

// newVHCIEventMapperWithLoader returns a mapper that defers topology
// resolution until the first VHCI-shaped event arrives. The loader is
// NOT invoked at construction — exporter-only deployments therefore
// never trigger a vhci_hcd sysfs read just to start the dispatcher.
func newVHCIEventMapperWithLoader(load topologyLoader) vhciEventMapper {
	return vhciEventMapper{
		load: load,
		mu:   &sync.Mutex{},
	}
}

// resolveTopology loads the topology on first call and caches the
// result. A failed load is NOT memoised — the next call retries so
// the mapper can recover after a transient sysfs error or vhci_hcd
// module reload. mu serialises concurrent callers (the dispatcher is
// single-threaded today, but the lock future-proofs fan-in).
func (m *vhciEventMapper) resolveTopology() (Topology, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.loaded {
		return m.topo, true
	}

	topo, err := m.load()
	if err != nil {
		return Topology{}, false
	}

	m.topo = topo
	m.loaded = true

	return m.topo, true
}

// mapEvent is the topology-aware entry point used by the dispatcher.
// It classifies a parsed uevent field map into a domain.Event using
// the cached Topology for the vhci devpath branch; non-vhci subsystems
// (usbip_host) bypass the topology entirely and produce device-level
// bind/unbind events — the VHCI topology loader is NOT consulted on
// the usbip_host path, so exporter-only deployments never fetch the
// vhci_hcd sysfs tree for their bind/unbind stream.
//
// Pointer receiver because mapVhciEvent may mutate the mapper's cached
// topology via resolveTopology's sync.Once gate.
func (m *vhciEventMapper) mapEvent(fields map[string]string) (domain.Event, bool) {
	if !isInterestingUevent(fields) {
		return nil, false
	}

	action := fields["ACTION"]

	devpath, ok := fields["DEVPATH"]
	if !ok {
		return nil, false
	}

	if fields["SUBSYSTEM"] == ueventSubsystemUSBIPHost {
		return mapUsbipHostEvent(action, devpath)
	}

	if fields["SUBSYSTEM"] == ueventSubsystemUDC {
		return mapUDCEvent(action, devpath)
	}

	return m.mapVhciEvent(action, devpath)
}

// udcDevpathPattern captures the UDC instance id from a UDC-shaped
// devpath. Kernel emits UDC events on the platform path
// `/devices/platform/usbip-vudc.<N>/udc/usbip-vudc.<N>` (configfs
// UDC-attribute write, observed on Linux 6.18) and also on the class
// path `/class/udc/usbip-vudc.<N>` (driver (un)load). The regex is
// scoped to usbip-vudc instances specifically — other UDC controllers
// (dwc3, chipidea, dummy_hcd, etc.) are not part of the usbip
// exporter surface, and emitting DeviceBoundEvent / DeviceUnboundEvent
// for their lifecycle would inject spurious signal into the reconnect
// watcher and session consumers.
var udcDevpathPattern = regexp.MustCompile(`/udc/(usbip-vudc\.\d+)$`)

// mapUDCEvent translates a SUBSYSTEM=udc uevent into a
// DeviceBoundEvent or DeviceUnboundEvent. The kernel emits KOBJ_CHANGE
// on configfs UDC-attribute transitions alongside the add/remove pair
// the class lifecycle emits; for the exporter-side bind/unbind
// observability we treat add+change as "bound" and remove as "unbound".
// The event carries the UDC's name (e.g. usbip-vudc.0) as its BusID
// because that is the handle configfs writers and the usbip tooling
// address the device by.
func mapUDCEvent(action, devpath string) (domain.Event, bool) {
	match := udcDevpathPattern.FindStringSubmatch(devpath)
	if match == nil {
		return nil, false
	}

	busID := domain.BusID(match[1])

	switch action {
	case ueventActionAdd, ueventActionChange:
		return domain.DeviceBoundEvent{
			At:     time.Now(),
			Device: domain.Device{BusID: busID, Path: devpath},
		}, true
	case ueventActionRemove:
		return domain.DeviceUnboundEvent{
			At:     time.Now(),
			Device: domain.Device{BusID: busID, Path: devpath},
		}, true
	default:
		return nil, false
	}
}

// mapVhciEvent handles the vhci_hcd-shaped devpath using the cached
// BusMap to translate (usbBus, rootPort1indexed) into a flat
// domain.PortID. A devpath whose usbN segment references a bus absent
// from the BusMap is treated as non-VHCI and skipped — the uevent came
// from a different HCD. A rootPort of zero violates the kernel's
// 1-indexed root-hub port numbering and is likewise rejected.
//
// The topology is resolved lazily here rather than at mapper
// construction. Exporter-only deployments (no vhci_hcd module) never
// receive a VHCI-shaped devpath, so the loader is never called; if a
// VHCI-shaped devpath does arrive and the loader fails (e.g.
// vhci_hcd sysfs group is absent), the event drops cleanly with
// ok=false — the Subscribe caller is not impacted, and usbip_host
// events continue to flow.
func (m *vhciEventMapper) mapVhciEvent(action, devpath string) (domain.Event, bool) {
	match := vhciDevpathPattern.FindStringSubmatch(devpath)
	if match == nil {
		return nil, false
	}

	usbBus, err := strconv.ParseUint(match[vhciDevpathGroupBus], 10, 32)
	if err != nil {
		return nil, false
	}

	rootPort1, err := strconv.ParseUint(match[vhciDevpathGroupRootPort], 10, 32)
	if err != nil || rootPort1 < 1 {
		return nil, false
	}

	topo, ok := m.resolveTopology()
	if !ok {
		return nil, false
	}

	loc, mapped := topo.BusMap[uint32(usbBus)]
	if !mapped {
		return nil, false
	}

	portID := topo.FlatPort(loc, uint32(rootPort1)-1)
	busID := domain.BusID(match[vhciDevpathGroupFullBusID])

	return vhciActionToEvent(action, portID, busID)
}

// vhciActionToEvent maps a uevent ACTION token to the corresponding
// domain event constructor. Unknown actions produce ok=false.
func vhciActionToEvent(action string, portID domain.PortID, busID domain.BusID) (domain.Event, bool) {
	switch action {
	case ueventActionAdd:
		return domain.PortAttachedEvent{
			At:   time.Now(),
			Port: domain.Port{ID: portID, BusID: busID},
		}, true
	case ueventActionRemove:
		return domain.PortDetachedEvent{
			At:     time.Now(),
			Port:   domain.Port{ID: portID, BusID: busID},
			Reason: "uevent",
		}, true
	case ueventActionChange:
		return domain.PortErroredEvent{
			At:   time.Now(),
			Port: domain.Port{ID: portID, BusID: busID},
			Err:  "usbip_status change",
		}, true
	default:
		return nil, false
	}
}

// parseUeventFields splits the NUL-separated payload into a map. The
// first token is ignored (kernel prepends ACTION@DEVPATH).
func parseUeventFields(payload []byte) map[string]string {
	out := make(map[string]string)
	tokens := splitNULBytes(payload)

	for i, tok := range tokens {
		if i == 0 {
			continue
		}

		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}

		out[k] = v
	}

	return out
}

// splitNULBytes splits payload on NUL bytes and returns the string
// pieces. Empty pieces are skipped.
func splitNULBytes(payload []byte) []string {
	parts := make([]string, 0, 8)
	start := 0

	for i, b := range payload {
		if b != 0 {
			continue
		}

		if i > start {
			parts = append(parts, string(payload[start:i]))
		}

		start = i + 1
	}

	if start < len(payload) {
		parts = append(parts, string(payload[start:]))
	}

	return parts
}

// Uevent SUBSYSTEM tokens the dispatcher cares about. Matched verbatim
// against the SUBSYSTEM= field of each parsed payload.
const (
	ueventSubsystemUSB       = "usb"
	ueventSubsystemUSBIPHost = "usbip_host"
	ueventSubsystemUSBHC     = "usb-hc"
)

// Uevent ACTION tokens emitted by the kernel for vhci_hcd-attached
// device lifecycle transitions. Other ACTION values are ignored by the
// mapper.
const (
	ueventActionAdd    = "add"
	ueventActionRemove = "remove"
	ueventActionChange = "change"
)

// ueventSubsystemUDC names the UDC class subsystem. Kernel emits add /
// remove uevents on /sys/class/udc/* when a UDC (such as usbip-vudc.N)
// is bound or released by configfs UDC attribute writes; the
// DeviceBoundEvent / DeviceUnboundEvent semantics extend naturally to
// that transition for the exporter side.
const ueventSubsystemUDC = "udc"

// isInterestingUevent filters for subsystems we care about:
// SUBSYSTEM=usb, SUBSYSTEM=usbip_host, SUBSYSTEM=usb-hc, SUBSYSTEM=udc.
// Everything else is ignored.
func isInterestingUevent(fields map[string]string) bool {
	sub := fields["SUBSYSTEM"]

	return sub == ueventSubsystemUSB ||
		sub == ueventSubsystemUSBIPHost ||
		sub == ueventSubsystemUSBHC ||
		sub == ueventSubsystemUDC
}

// vhciDevpathPattern matches the vhci-managed USB devpath shape:
//
//	/devices/platform/vhci_hcd.<M>/usb<N>/<BusID>
//
// The controller suffix M may be any decimal — multi-controller
// kernels emit vhci_hcd.1, vhci_hcd.2, and so on. The bus ID follows
// the Linux USB topology grammar (pkg/domain/busid.go:18): one or more
// decimal digits, a dash, then a dot-separated sequence of decimal
// numbers. Hub-attached devices therefore have dotted bus IDs like
// "1-1.2" or "2-3.4.5"; the regex captures the full busid verbatim
// for the emitted event while also decomposing the (bus, rootPort)
// pair for topology lookup. Capturing groups:
//
//	[1] usbBus — the number after "usb" in the path segment.
//	[2] fullBusID — the entire "<bus>-<port>[.hub...]" suffix.
//	[3] rootPort1indexed — the integer between the first '-' and the
//	    first '.' (or end-of-segment for non-hub devices).
//
// The pattern is anchored at both ends. Without the trailing "$"
// anchor, FindStringSubmatch would match any DEVPATH that merely
// starts with the expected prefix — so a USB interface sub-path such
// as "/devices/platform/vhci_hcd.0/usb1/1-1/1-1:1.0" would truncate to
// busid "1-1" and emit a spurious PortDetachedEvent on interface-level
// unbind, mid-terminating an active exporter session. The leading "^"
// is symmetric belt-and-suspenders: the uevent payload always delivers
// a full absolute DEVPATH, so a leading-anchor mismatch cannot arise
// in production, but start-anchoring makes the regex intent explicit.
var vhciDevpathPattern = regexp.MustCompile(`^/devices/platform/vhci_hcd\.\d+/usb(\d+)/(\d+-(\d+)(?:\.\d+)*)$`)

// Regex group indices for vhciDevpathPattern. Named so the extraction
// code reads without magic numbers.
const (
	vhciDevpathGroupBus       = 1
	vhciDevpathGroupFullBusID = 2
	vhciDevpathGroupRootPort  = 3
)

// usbipHostBusIDPattern captures the trailing bus-id segment of a
// usbip_host DEVPATH. The upstream kernel emits add/remove uevents on
// the bound device's sysfs node when the usbip_host driver binds or
// releases it; the bus id is the final path segment and follows the
// domain busid grammar (pkg/domain/busid.go:18). Unlike the vhci
// devpath, there is no vhci_hcd prefix — the device sits at its
// native sysfs location.
var usbipHostBusIDPattern = regexp.MustCompile(`/(\d+-\d+(?:\.\d+)*)$`)

// mapUsbipHostEvent handles the usbip_host-shaped devpath (local
// exporter side bind/unbind). add → DeviceBoundEvent (device became
// exportable); remove → DeviceUnboundEvent (device returned to its
// original driver). Without this classifier every session.go /
// importer.go / cmd/usbip-go branch that acts on these event types
// would be unreachable.
func mapUsbipHostEvent(action, devpath string) (domain.Event, bool) {
	match := usbipHostBusIDPattern.FindStringSubmatch(devpath)
	if match == nil {
		return nil, false
	}

	busID := domain.BusID(match[1])

	switch action {
	case ueventActionAdd:
		return domain.DeviceBoundEvent{
			At:     time.Now(),
			Device: domain.Device{BusID: busID, Path: devpath},
		}, true
	case ueventActionRemove:
		return domain.DeviceUnboundEvent{
			At:     time.Now(),
			Device: domain.Device{BusID: busID, Path: devpath},
		}, true
	default:
		return nil, false
	}
}
