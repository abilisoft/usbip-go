//go:build linux

package kernel_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// fakeSocket is a channel-backed NetlinkSocket that emits pre-canned
// uevent payloads under test control. Multiple Subscribers share the
// same underlying socket via the fan-out EventsAdapter maintains.
type fakeSocket struct {
	mu      sync.Mutex
	payload chan []byte
	closed  atomic.Bool
}

func newFakeSocket() *fakeSocket {
	return &fakeSocket{payload: make(chan []byte, 16)}
}

// Receive blocks until a payload is available or Close is called.
func (s *fakeSocket) Receive() ([]byte, error) {
	for {
		if s.closed.Load() {
			return nil, io.EOF
		}

		select {
		case p, ok := <-s.payload:
			if !ok {
				return nil, io.EOF
			}

			return p, nil
		case <-time.After(10 * time.Millisecond):
			// spin to re-check closed
		}
	}
}

// Close idempotently shuts the socket. A second call is a no-op.
func (s *fakeSocket) Close() error {
	if s.closed.Swap(true) {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	close(s.payload)

	return nil
}

// feed pushes an event payload for Receive to return.
func (s *fakeSocket) feed(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed.Load() {
		return
	}

	s.payload <- p
}

// uevent constructs a NUL-separated KEY=VALUE buffer. The kernel's
// kobject_uevent format puts the first token as ACTION@DEVPATH then
// subsequent lines as KEY=VALUE\x00...; the parser we ship ignores
// the first token and reads only KEY=VALUE tokens.
func uevent(fields map[string]string) []byte {
	out := make([]byte, 0, 256)

	// Kernel prepends "ACTION@DEVPATH" as the first NUL-terminated
	// token; include an ignored placeholder for realism.
	header := fields["ACTION"] + "@" + fields["DEVPATH"]

	out = append(out, []byte(header)...)
	out = append(out, 0)

	for k, v := range fields {
		out = append(out, []byte(k+"="+v)...)
		out = append(out, 0)
	}

	return out
}

// newAdapterWithFakeSocket returns an EventsAdapter wired up with a
// fakeSocket dialer AND the canonical single-controller topology
// fixture. Every Subscribe path now loads Topology via the injected
// fs.FS; tests that want a concrete flat Port.ID assert against the
// (ControllerIdx=0, Hub=HS) bus=1 / (ControllerIdx=0, Hub=SS) bus=2
// mapping.
func newAdapterWithFakeSocket(t *testing.T) (*kernel.EventsAdapter, *fakeSocket) {
	t.Helper()

	sock := newFakeSocket()
	dialer := func() (kernel.NetlinkSocket, error) { return sock, nil }

	a, err := kernel.NewEventsAdapter(
		kernel.WithFS(singleControllerTopoFS()),
		kernel.WithNetlinkDialer(dialer),
	)
	require.NoError(t, err)

	return a, sock
}

func TestSubscribe_DeliversParsedEvent(t *testing.T) {
	t.Parallel()

	a, sock := newAdapterWithFakeSocket(t)

	defer func() { _ = sock.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, unsub, err := a.Subscribe(ctx)
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.NotNil(t, unsub)

	sock.feed(uevent(map[string]string{
		"ACTION":    "remove",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-5",
	}))

	select {
	case ev := <-ch:
		require.NotNil(t, ev)
		require.Equal(t, domain.EventPortDetached, ev.EventKind())
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}

	unsub()
}

func TestSubscribe_FanOutToTwoConsumers(t *testing.T) {
	t.Parallel()

	a, sock := newAdapterWithFakeSocket(t)

	defer func() { _ = sock.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch1, unsub1, err := a.Subscribe(ctx)
	require.NoError(t, err)

	ch2, unsub2, err := a.Subscribe(ctx)
	require.NoError(t, err)

	sock.feed(uevent(map[string]string{
		"ACTION":    "add",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-1",
	}))

	seenCount := 0

	for _, c := range []<-chan domain.Event{ch1, ch2} {
		select {
		case ev := <-c:
			require.NotNil(t, ev)

			seenCount++
		case <-time.After(2 * time.Second):
			t.Fatal("one subscriber did not receive the event")
		}
	}

	require.Equal(t, 2, seenCount)

	unsub1()
	unsub2()
}

func TestSubscribe_CancelStopsConsumer(t *testing.T) {
	t.Parallel()

	a, sock := newAdapterWithFakeSocket(t)

	defer func() { _ = sock.Close() }()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, unsub, err := a.Subscribe(ctx)
	require.NoError(t, err)

	unsub()

	// Channel must close promptly.
	select {
	case _, ok := <-ch:
		require.False(t, ok, "channel must close after unsub")
	case <-time.After(1 * time.Second):
		t.Fatal("channel did not close after unsub")
	}
}

// TestSubscribe_ExplicitUnsubReleasesCtxWatcher drives the explicit
// unsub path: the caller subscribes with a long-lived ctx (not
// cancelled for the lifetime of the test), calls the returned unsub
// func, and returns. With the pre-fix code, the per-subscription
// ctx-watcher goroutine is still parked on ctx.Done() after unsub ran,
// so goleak.VerifyTestMain flags it at suite teardown. With the fix,
// unsub signals the watcher to exit, goleak is quiet.
//
// Using context.Background directly (rather than t.Context()) is
// intentional: t.Context() is cancelled when the test returns, which
// would mask the leak we are hunting.
func TestSubscribe_ExplicitUnsubReleasesCtxWatcher(t *testing.T) {
	t.Parallel()

	a, sock := newAdapterWithFakeSocket(t)

	defer func() { _ = sock.Close() }()

	ch, unsub, err := a.Subscribe(context.Background())
	require.NoError(t, err)
	require.NotNil(t, ch)

	unsub()

	// Drain the channel so the test is not subject to scheduling
	// timing on the close signal.
	select {
	case _, ok := <-ch:
		require.False(t, ok, "channel must close after unsub")
	case <-time.After(1 * time.Second):
		t.Fatal("channel did not close after unsub")
	}
}

// TestSubscribe_DialFailurePropagates confirms a dialer error surfaces
// to the caller.
func TestSubscribe_DialFailurePropagates(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewEventsAdapter(
		kernel.WithFS(singleControllerTopoFS()),
		kernel.WithNetlinkDialer(
			func() (kernel.NetlinkSocket, error) {
				return nil, errDialFailed
			},
		),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, _, err = a.Subscribe(ctx)
	require.ErrorIs(t, err, errDialFailed)
}

var errDialFailed = errors.New("fake dial failed")

// TestSubscribe_FirstSubscriberCancelDoesNotStopOthers exercises the
// spec §5.1 "last Unsubscribe stops it" contract. If the first
// subscriber's ctx cancellation tore down the dispatcher, remaining
// subscribers would stop receiving events. Codex Phase 4 review
// finding 1.
func TestSubscribe_FirstSubscriberCancelDoesNotStopOthers(t *testing.T) {
	t.Parallel()

	a, sock := newAdapterWithFakeSocket(t)

	defer func() { _ = sock.Close() }()

	// Subscriber 1: cancellable.
	ctx1, cancel1 := context.WithCancel(t.Context())
	ch1, _, err := a.Subscribe(ctx1)
	require.NoError(t, err)

	// Subscriber 2: uses test ctx (no early cancel).
	ch2, unsub2, err := a.Subscribe(t.Context())
	require.NoError(t, err)

	defer unsub2()

	// Drain ch1 in the background so we do not fill its buffer before
	// cancel1 fires. Count is reported so revive does not flag the
	// channel range as an empty block.
	drain1Done := make(chan int, 1)

	go func() {
		count := 0

		for range ch1 {
			count++
		}

		drain1Done <- count
	}()

	// Cancel subscriber 1. Its channel must close; the dispatcher must
	// keep running for subscriber 2.
	cancel1()
	<-drain1Done

	// Feed an event AFTER subscriber 1 has been torn down.
	sock.feed(uevent(map[string]string{
		"ACTION":    "add",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-1",
		"SUBSYSTEM": "usb",
	}))

	select {
	case ev, ok := <-ch2:
		require.True(t, ok, "subscriber 2 channel must stay open after subscriber 1 cancels")

		_, isAttach := ev.(domain.PortAttachedEvent)
		require.True(t, isAttach, "expected PortAttachedEvent, got %T", ev)
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber 2 did not get the event after subscriber 1 cancelled — dispatcher torn down prematurely")
	}
}

// TestSubscribe_UsbipHostEmitsDeviceBindEvents drives the usbip_host
// bind/unbind notification shape. Pre pass-3 RANK 3, mapUeventToDomain
// only produced vhci_hcd-shaped Port* events and never returned a
// DeviceBoundEvent / DeviceUnboundEvent. Downstream consumers
// (cmd/usbip/events.go, session.go, importer.go) that branch on those
// event types were dead code. Post-fix: SUBSYSTEM=usbip_host
// ACTION=add → DeviceBoundEvent; ACTION=remove → DeviceUnboundEvent;
// the bus ID is the trailing path segment that matches the domain
// busid topology pattern.
func TestSubscribe_UsbipHostEmitsDeviceBindEvents(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		action    string
		devpath   string
		wantKind  domain.EventKind
		wantBusID domain.BusID
	}{
		{
			name:      "bind_simple",
			action:    "add",
			devpath:   "/devices/pci0000:00/0000:00:14.0/usb1/1-1",
			wantKind:  domain.EventDeviceBound,
			wantBusID: domain.BusID("1-1"),
		},
		{
			name:      "unbind_simple",
			action:    "remove",
			devpath:   "/devices/pci0000:00/0000:00:14.0/usb1/1-1",
			wantKind:  domain.EventDeviceUnbound,
			wantBusID: domain.BusID("1-1"),
		},
		{
			name:      "bind_dotted",
			action:    "add",
			devpath:   "/devices/pci0000:00/0000:00:14.0/usb1/1-1.2",
			wantKind:  domain.EventDeviceBound,
			wantBusID: domain.BusID("1-1.2"),
		},
		{
			name:      "unbind_dotted",
			action:    "remove",
			devpath:   "/devices/pci0000:00/0000:00:14.0/usb2/2-3.4.5",
			wantKind:  domain.EventDeviceUnbound,
			wantBusID: domain.BusID("2-3.4.5"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, sock := newAdapterWithFakeSocket(t)

			defer func() { _ = sock.Close() }()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			ch, unsub, err := a.Subscribe(ctx)
			require.NoError(t, err)

			defer unsub()

			sock.feed(uevent(map[string]string{
				"ACTION":    tc.action,
				"SUBSYSTEM": "usbip_host",
				"DEVPATH":   tc.devpath,
			}))

			select {
			case ev := <-ch:
				require.NotNil(t, ev)
				require.Equal(t, tc.wantKind, ev.EventKind(),
					"usbip_host %s devpath %q must produce %s", tc.action, tc.devpath, tc.wantKind)

				switch e := ev.(type) {
				case domain.DeviceBoundEvent:
					require.Equal(t, tc.wantBusID, e.Device.BusID)
				case domain.DeviceUnboundEvent:
					require.Equal(t, tc.wantBusID, e.Device.BusID)
				default:
					t.Fatalf("unexpected event type %T", ev)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for usbip_host event (action=%s devpath=%q)", tc.action, tc.devpath)
			}
		})
	}
}

// TestSubscribe_DottedBusIDProducesEvent drives the dotted-topology
// parse path. Hub-attached devices have bus IDs like "1-1.2" or
// "2-3.4.5"; pre-fix the devpath regex only matched the simple "N-P"
// shape so ANY hub-attached device silently skipped the event map.
// The domain BusID pattern accepts the full dotted form, so the
// adapter must too. Covers pass-3 RANK 1.
func TestSubscribe_DottedBusIDProducesEvent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		action    string
		devpath   string
		wantKind  domain.EventKind
		wantBusID domain.BusID
	}{
		{
			name:      "remove_one_dot",
			action:    "remove",
			devpath:   "/devices/platform/vhci_hcd.0/usb1/1-1.2",
			wantKind:  domain.EventPortDetached,
			wantBusID: domain.BusID("1-1.2"),
		},
		{
			name:      "add_two_dots",
			action:    "add",
			devpath:   "/devices/platform/vhci_hcd.0/usb2/2-3.4.5",
			wantKind:  domain.EventPortAttached,
			wantBusID: domain.BusID("2-3.4.5"),
		},
		{
			name:      "change_one_dot",
			action:    "change",
			devpath:   "/devices/platform/vhci_hcd.0/usb1/1-2.3",
			wantKind:  domain.EventPortErrored,
			wantBusID: domain.BusID("1-2.3"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, sock := newAdapterWithFakeSocket(t)

			defer func() { _ = sock.Close() }()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			ch, unsub, err := a.Subscribe(ctx)
			require.NoError(t, err)

			defer unsub()

			sock.feed(uevent(map[string]string{
				"ACTION":    tc.action,
				"SUBSYSTEM": "usb",
				"DEVPATH":   tc.devpath,
			}))

			select {
			case ev := <-ch:
				require.NotNil(t, ev)
				require.Equal(t, tc.wantKind, ev.EventKind(),
					"dotted devpath %q must still produce %s", tc.devpath, tc.wantKind)

				switch e := ev.(type) {
				case domain.PortAttachedEvent:
					require.Equal(t, tc.wantBusID, e.Port.BusID)
				case domain.PortDetachedEvent:
					require.Equal(t, tc.wantBusID, e.Port.BusID)
				case domain.PortErroredEvent:
					require.Equal(t, tc.wantBusID, e.Port.BusID)
				default:
					t.Fatalf("unexpected event type %T", ev)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("timed out waiting for event from dotted devpath %q", tc.devpath)
			}
		})
	}
}

// TestSubscribe_FailsWhenTopologyUnavailable pins the Task-3 wiring
// contract: Subscribe must load the VHCI topology before accepting
// subscribers so every downstream event already knows its flat
// Port.ID. A host where vhci_hcd is not loaded (or sysfs is missing)
// yields an errTopologyNoControllers / missing-nports failure that
// Subscribe must surface — proceeding with an empty BusMap would
// silently drop every vhci event as "non-VHCI bus".
func TestSubscribe_FailsWhenTopologyUnavailable(t *testing.T) {
	t.Parallel()

	sock := newFakeSocket()

	defer func() { _ = sock.Close() }()

	dialer := func() (kernel.NetlinkSocket, error) { return sock, nil }

	// Empty MapFS — no vhci_hcd.0/nports, so discoverTopology fails at
	// the first sysfs read.
	a, err := kernel.NewEventsAdapter(
		kernel.WithFS(topoFS(nil)),
		kernel.WithNetlinkDialer(dialer),
	)
	require.NoError(t, err)

	_, _, err = a.Subscribe(t.Context())
	require.Error(t, err,
		"Subscribe must fail when the VHCI topology cannot be loaded")
}

// TestSubscribe_EmitsFlatPortIDForVhciEvent is the end-to-end contract
// test for the Task-3 wiring: an event delivered through the
// dispatcher must carry the kernel's flat Port.ID (BusMap-resolved)
// rather than the legacy leading-busid-segment value. Devpath
// "/vhci_hcd.0/usb1/1-5" on a single-controller topology (BusMap[1] =
// (0, HS), HCPorts=8, VHCIPorts=16) must flatten to Port.ID = 0*16 +
// 0 + (5-1) = 4. The legacy extractPortFromBusID returned 5, so any
// downstream consumer relying on the dispatcher's Port.ID was
// unaligned with the kernel's status file.
func TestSubscribe_EmitsFlatPortIDForVhciEvent(t *testing.T) {
	t.Parallel()

	sock := newFakeSocket()

	defer func() { _ = sock.Close() }()

	dialer := func() (kernel.NetlinkSocket, error) { return sock, nil }

	a, err := kernel.NewEventsAdapter(
		kernel.WithFS(singleControllerTopoFS()),
		kernel.WithNetlinkDialer(dialer),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, unsub, err := a.Subscribe(ctx)
	require.NoError(t, err)

	defer unsub()

	sock.feed(uevent(map[string]string{
		"ACTION":    "add",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-5",
	}))

	select {
	case ev := <-ch:
		attach, ok := ev.(domain.PortAttachedEvent)
		require.True(t, ok, "expected PortAttachedEvent, got %T", ev)
		require.Equal(t, domain.PortID(4), attach.Port.ID,
			"rhport0=4 on HS hub of controller 0 must flatten to Port.ID=4")
		require.Equal(t, domain.BusID("1-5"), attach.Port.BusID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for flat-port event")
	}
}

// TestSubscribe_EmitsFlatPortIDForMultiController covers the multi-
// controller flat-port arithmetic end-to-end: devpath through
// vhci_hcd.1/usb4 (controller 1, SS hub) with rootPort=2 → rhport0=1
// → flat Port.ID = 1*16 + 8 + 1 = 25. The legacy path hard-coded
// vhci_hcd.0 in the regex and lost the multi-controller axis entirely.
func TestSubscribe_EmitsFlatPortIDForMultiController(t *testing.T) {
	t.Parallel()

	sock := newFakeSocket()

	defer func() { _ = sock.Close() }()

	dialer := func() (kernel.NetlinkSocket, error) { return sock, nil }

	a, err := kernel.NewEventsAdapter(
		kernel.WithFS(dualControllerTopoFS()),
		kernel.WithNetlinkDialer(dialer),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ch, unsub, err := a.Subscribe(ctx)
	require.NoError(t, err)

	defer unsub()

	sock.feed(uevent(map[string]string{
		"ACTION":    "remove",
		"SUBSYSTEM": "usb",
		"DEVPATH":   "/devices/platform/vhci_hcd.1/usb4/4-2",
	}))

	select {
	case ev := <-ch:
		detach, ok := ev.(domain.PortDetachedEvent)
		require.True(t, ok, "expected PortDetachedEvent, got %T", ev)
		require.Equal(t, domain.PortID(25), detach.Port.ID,
			"rhport0=1 on SS hub of controller 1 must flatten to Port.ID=25")
		require.Equal(t, domain.BusID("4-2"), detach.Port.BusID)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for multi-controller flat-port event")
	}
}

// TestSubscribe_RegistrationRaceDoesNotDropEvent drives the window
// between Subscribe returning and the first event arriving. The
// dispatcher must not be receiving events before the first subscriber
// is registered in the fan-out map. Codex Phase 4 review finding 2.
func TestSubscribe_RegistrationRaceDoesNotDropEvent(t *testing.T) {
	t.Parallel()

	a, sock := newAdapterWithFakeSocket(t)

	defer func() { _ = sock.Close() }()

	// Pre-seed the socket so an event is available the instant the
	// dispatcher's run-loop starts. If the loop starts before
	// addSubscriber lands, this event is broadcast to zero subscribers
	// and lost.
	sock.feed(uevent(map[string]string{
		"ACTION":    "add",
		"DEVPATH":   "/devices/platform/vhci_hcd.0/usb1/1-1",
		"SUBSYSTEM": "usb",
	}))

	ch, unsub, err := a.Subscribe(t.Context())
	require.NoError(t, err)

	defer unsub()

	select {
	case ev, ok := <-ch:
		require.True(t, ok, "channel closed before event arrived")

		_, isAttach := ev.(domain.PortAttachedEvent)
		require.True(t, isAttach, "expected PortAttachedEvent, got %T", ev)
	case <-time.After(2 * time.Second):
		t.Fatal("event pre-seeded before Subscribe() was dropped — registration race")
	}
}
