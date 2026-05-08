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
// fakeSocket dialer.
func newAdapterWithFakeSocket(t *testing.T) (*kernel.EventsAdapter, *fakeSocket) {
	t.Helper()

	sock := newFakeSocket()
	dialer := func() (kernel.NetlinkSocket, error) { return sock, nil }

	a, err := kernel.NewEventsAdapter(kernel.WithNetlinkDialer(dialer))
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

// TestSubscribe_DialFailurePropagates confirms a dialer error surfaces
// to the caller.
func TestSubscribe_DialFailurePropagates(t *testing.T) {
	t.Parallel()

	a, err := kernel.NewEventsAdapter(kernel.WithNetlinkDialer(
		func() (kernel.NetlinkSocket, error) {
			return nil, errDialFailed
		},
	))
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, _, err = a.Subscribe(ctx)
	require.ErrorIs(t, err, errDialFailed)
}

var errDialFailed = errors.New("fake dial failed")
