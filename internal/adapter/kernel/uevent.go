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

// realNetlinkSocket is the production NetlinkSocket implementation.
type realNetlinkSocket struct {
	fd int
}

// Receive blocks until one uevent payload is delivered.
func (s *realNetlinkSocket) Receive() ([]byte, error) {
	buf := make([]byte, netlinkUeventBufSize)

	n, _, err := unix.Recvfrom(s.fd, buf, 0)
	if err != nil {
		return nil, fmt.Errorf("recvfrom netlink: %w", err)
	}

	return buf[:n], nil
}

// Close releases the socket fd.
func (s *realNetlinkSocket) Close() error {
	err := unix.Close(s.fd)
	if err != nil {
		return fmt.Errorf("close netlink socket: %w", err)
	}

	return nil
}

// openRealNetlinkSocket opens and binds a real
// AF_NETLINK/NETLINK_KOBJECT_UEVENT socket.
func openRealNetlinkSocket() (*realNetlinkSocket, error) {
	fd, err := unix.Socket(unix.AF_NETLINK, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, fmt.Errorf("open netlink socket: %w", err)
	}

	sa := &unix.SockaddrNetlink{
		Family: unix.AF_NETLINK,
		Groups: netlinkUdevGroup,
	}

	err = unix.Bind(fd, sa)
	if err != nil {
		_ = unix.Close(fd)

		return nil, fmt.Errorf("bind netlink socket: %w", err)
	}

	return &realNetlinkSocket{fd: fd}, nil
}

// eventDispatcher owns the single netlink socket and the fan-out list
// of subscriber channels. EventsAdapter lazily constructs one on the
// first Subscribe call and tears it down on the last Unsubscribe.
//
// Shutdown uses a stop channel (closed by cancel()) instead of a
// stored context so the struct is not `containedctx`-flagged and the
// dispatcher's lifetime is unambiguously independent of any caller's
// ctx. done closes when run() actually exits, so tearDown can block
// on orderly teardown.
type eventDispatcher struct {
	mu          sync.Mutex
	sock        NetlinkSocket
	subscribers map[int64]chan domain.Event
	nextID      int64
	stop        chan struct{}
	stopOnce    sync.Once
	done        chan struct{}
	logger      *slogRef
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
// lives until the LAST subscriber unsubscribes (spec §5.1), not the
// first — the dispatcher carries its own context independent of any
// subscriber's.
//
// Registration ordering: the subscriber is added to the fan-out map
// BEFORE the run-loop goroutine is kicked off on the first call, so
// there is no window in which events could be broadcast to an empty
// subscriber set.
func (a *EventsAdapter) Subscribe(ctx context.Context) (<-chan domain.Event, func(), error) {
	// NewEventsAdapter eagerly allocates dispMu so concurrent first-
	// Subscribers cannot race the pointer write (RANK 4). Lock the
	// shared mutex directly.
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
func (a *EventsAdapter) ensureDispatcher() (*eventDispatcher, bool, error) {
	if a.disp != nil {
		return a.disp, false, nil
	}

	sock, err := a.nlDial()
	if err != nil {
		return nil, false, fmt.Errorf("dial netlink: %w", err)
	}

	d := newDispatcher(sock, a.logger)

	a.disp = d

	return d, true, nil
}

// newDispatcher constructs an eventDispatcher whose lifetime is
// controlled by a stop channel (closed by cancel()). No subscriber
// can shorten the dispatcher's life.
func newDispatcher(sock NetlinkSocket, logger *slog.Logger) *eventDispatcher {
	return &eventDispatcher{
		sock:        sock,
		subscribers: make(map[int64]chan domain.Event),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
		logger: &slogRef{
			warn: func(msg string, args ...any) { logger.Warn(msg, args...) },
		},
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

		payload, err := d.sock.Receive()
		if err != nil {
			d.handleReceiveErr(err)

			return
		}

		ev, ok := parseUevent(payload)
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

// parseUevent tokenises a NUL-separated KEY=VALUE payload and maps it
// to a domain.Event. Returns ok=false on unrecognised subsystems /
// unmappable payloads. The interface-typed return is mandatory:
// domain.Event is a closed union, and this function's job is
// precisely to produce a value of that union.
func parseUevent(payload []byte) (domain.Event, bool) {
	fields := parseUeventFields(payload)

	if !isInterestingUevent(fields) {
		return nil, false
	}

	return mapUeventToDomain(fields)
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

// isInterestingUevent filters for subsystems we care about:
// SUBSYSTEM=usb and SUBSYSTEM=usbip_host. Everything else is ignored.
func isInterestingUevent(fields map[string]string) bool {
	sub := fields["SUBSYSTEM"]

	return sub == "usb" || sub == "usbip_host" || sub == "usb-hc"
}

// vhciDevpathPattern matches the vhci-managed USB devpath shape:
//
//	/devices/platform/vhci_hcd.0/usb<N>/<BusID>
//
// The bus ID follows the Linux USB topology grammar
// (pkg/domain/busid.go:18): one or more decimal digits, a dash, then
// a dot-separated sequence of decimal numbers. Hub-attached devices
// therefore have dotted bus IDs like "1-1.2" or "2-3.4.5"; pre-fix
// this regex only captured the leading "N-P" pair and silently
// truncated every hub-attached device's busid. Capturing groups:
// (bus, bus_id).
var vhciDevpathPattern = regexp.MustCompile(`/devices/platform/vhci_hcd\.0/usb(\d+)/(\d+-\d+(?:\.\d+)*)`)

// mapUeventToDomain maps a parsed uevent fields map into a domain
// event. Missing ACTION or non-USB paths return ok=false so the caller
// skips. Time is set from the wall clock — the adapter does not carry
// a clock for netlink events.
func mapUeventToDomain(fields map[string]string) (domain.Event, bool) {
	action := fields["ACTION"]

	devpath, ok := fields["DEVPATH"]
	if !ok {
		return nil, false
	}

	match := vhciDevpathPattern.FindStringSubmatch(devpath)
	if match == nil {
		return nil, false
	}

	portBusID := match[2]
	port := extractPortFromBusID(portBusID)

	switch action {
	case "add":
		return domain.PortAttachedEvent{
			At:   time.Now(),
			Port: domain.Port{ID: port, BusID: domain.BusID(portBusID)},
		}, true
	case "remove":
		return domain.PortDetachedEvent{
			At:     time.Now(),
			Port:   domain.Port{ID: port, BusID: domain.BusID(portBusID)},
			Reason: "uevent",
		}, true
	case "change":
		return domain.PortErroredEvent{
			At:   time.Now(),
			Port: domain.Port{ID: port, BusID: domain.BusID(portBusID)},
			Err:  "usbip_status change",
		}, true
	default:
		return nil, false
	}
}

// busIDSplitCount is the expected piece count when splitting a busid
// on the first '-': "<bus>-<port>" → 2 pieces.
const busIDSplitCount = 2

// extractPortFromBusID parses "N-P" and returns the port number as a
// domain.PortID. A malformed input falls through to zero; the caller
// still gets the busid in the event body for correlation.
func extractPortFromBusID(busID string) domain.PortID {
	parts := strings.SplitN(busID, "-", busIDSplitCount)

	if len(parts) != busIDSplitCount {
		return 0
	}

	v, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return 0
	}

	return domain.PortID(v)
}
