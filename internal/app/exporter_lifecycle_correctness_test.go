// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

var (
	errFirstDisconnect  = errors.New("first disconnect failed")
	errSecondDisconnect = errors.New("second disconnect failed")
)

type messageSignalHandler struct {
	message string
	done    chan struct{}
	once    sync.Once
}

func (h *messageSignalHandler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *messageSignalHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message == h.message {
		h.once.Do(func() { close(h.done) })
	}

	return nil
}

func (h *messageSignalHandler) WithAttrs([]slog.Attr) slog.Handler {
	return h
}

func (h *messageSignalHandler) WithGroup(string) slog.Handler {
	return h
}

// exporterKernelActivityStub adds the optional activity-probe capability to
// the generator-owned ExporterKernelMock without editing generated code.
type exporterKernelActivityStub struct {
	*ExporterKernelMock

	activeFunc func(context.Context, domain.BusID) (bool, error)
}

func (s *exporterKernelActivityStub) ExportSessionActive(
	ctx context.Context, busID domain.BusID,
) (bool, error) {
	return s.activeFunc(ctx, busID)
}

// exportPollClock reports exactly when the Session loop arms its first poll,
// allowing tests to advance the FakeClock without racing timer registration.
type exportPollClock struct {
	*testutil.FakeClock

	interval time.Duration
	armed    chan struct{}
	once     sync.Once
}

func (c *exportPollClock) After(d time.Duration) <-chan time.Time {
	ch := c.FakeClock.After(d)
	if d == c.interval {
		c.once.Do(func() { close(c.armed) })
	}

	return ch
}

func newExportPollClock(interval time.Duration) *exportPollClock {
	return &exportPollClock{
		FakeClock: testutil.NewFakeClockAt(exporterTestEpoch()),
		interval:  interval,
		armed:     make(chan struct{}),
	}
}

func TestExporterSession_StatusProbeEndsSessionWithoutEvent(t *testing.T) {
	t.Parallel()

	const (
		busID        = domain.BusID("6-1")
		pollInterval = 37 * time.Millisecond
	)

	clock := newExportPollClock(pollInterval)
	probeCalled := make(chan struct{})
	exportReturned := make(chan struct{})

	baseKernel := &ExporterKernelMock{
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: busID}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, got domain.BusID) error {
			require.Equal(t, busID, got)
			close(exportReturned)

			return nil
		},
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error { return nil },
	}

	kernel := &exporterKernelActivityStub{
		ExporterKernelMock: baseKernel,
		activeFunc: func(_ context.Context, got domain.BusID) (bool, error) {
			require.Equal(t, busID, got)
			close(probeCalled)

			return false, nil
		},
	}

	events := make(chan domain.Event)
	eventCancel := sync.OnceFunc(func() { close(events) })
	exp := newExporterForTest(
		t,
		app.WithExporterKernel(kernel),
		app.WithExporterEvents(&KernelEventsMock{
			SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
				return events, eventCancel, nil
			},
		}),
		app.WithExporterCodec(newSessionImportCodec(busID)),
		app.WithExporterClock(clock),
		app.WithExporterStatusPollInterval(pollInterval),
	)

	listener := newCountingListener()
	serveCtx, cancelServe := context.WithCancel(context.Background())
	t.Cleanup(cancelServe)

	serveDone := make(chan error, 1)
	go func() { serveDone <- exp.Serve(serveCtx, listener) }()

	client, err := listener.dial(serveCtx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	select {
	case <-exportReturned:
	case <-time.After(2 * time.Second):
		t.Fatal("ExportOnConn did not return")
	}

	select {
	case <-clock.armed:
	case <-time.After(2 * time.Second):
		t.Fatal("session activity poll was not armed")
	}

	clock.Advance(pollInterval)

	select {
	case <-probeCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("session activity probe was not called")
	}

	serverConns := listener.snapshot()
	require.Len(t, serverConns, 1)

	select {
	case <-serverConns[0].closedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("inactive status did not release the accepted connection")
	}

	require.Empty(t, exp.Sessions(context.Background()))

	cancelServe()
	require.NoError(t, <-serveDone)
	require.NoError(t, exp.Shutdown(context.Background()))
	require.Empty(t, baseKernel.DisconnectCalls(),
		"natural peer completion must not issue a shutdown Disconnect")
}

func TestExporterShutdown_WinsInactiveProbeRace(t *testing.T) {
	t.Parallel()

	const (
		busID        = domain.BusID("6-2")
		pollInterval = 41 * time.Millisecond
	)

	clock := newExportPollClock(pollInterval)
	probeEntered := make(chan struct{})
	releaseProbe := make(chan struct{})
	disconnectCalled := make(chan struct{})

	baseKernel := &ExporterKernelMock{
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: busID}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			return nil
		},
		DisconnectFunc: func(_ context.Context, _ domain.BusID) error {
			close(disconnectCalled)

			return nil
		},
	}
	kernel := &exporterKernelActivityStub{
		ExporterKernelMock: baseKernel,
		activeFunc: func(_ context.Context, _ domain.BusID) (bool, error) {
			close(probeEntered)
			<-releaseProbe

			return false, nil
		},
	}

	events := make(chan domain.Event)
	exp := newExporterForTest(
		t,
		app.WithExporterKernel(kernel),
		app.WithExporterEvents(&KernelEventsMock{
			SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
				return events, sync.OnceFunc(func() { close(events) }), nil
			},
		}),
		app.WithExporterCodec(newSessionImportCodec(busID)),
		app.WithExporterClock(clock),
		app.WithExporterStatusPollInterval(pollInterval),
	)

	watchCtx, cancelWatch := context.WithCancel(context.Background())
	t.Cleanup(cancelWatch)

	observed := make(chan domain.Event, 4)

	go func() {
		for event := range exp.WatchSessions(watchCtx) {
			observed <- event
		}
	}()

	require.Eventually(t, func() bool {
		return app.SessionSubscribersLenForTest(exp) == 1
	}, 2*time.Second, time.Millisecond)

	listener := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 62), Port: 9062})

	serveDone := make(chan error, 1)
	go func() { serveDone <- exp.Serve(context.Background(), listener) }()

	client, err := listener.dial(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	_, err = client.Write(opHeader(wire.OpReqImport))
	require.NoError(t, err)

	select {
	case <-clock.armed:
	case <-time.After(2 * time.Second):
		t.Fatal("session activity poll was not armed")
	}

	clock.Advance(pollInterval)

	select {
	case <-probeEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("session activity probe did not block")
	}

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- exp.Shutdown(context.Background()) }()

	select {
	case <-disconnectCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown did not claim Disconnect ownership")
	}

	close(releaseProbe)
	require.NoError(t, <-shutdownDone)
	require.NoError(t, <-serveDone)

	var ended domain.SessionEndedEvent

	for event := range observed {
		if got, ok := event.(domain.SessionEndedEvent); ok {
			ended = got
			break
		}
	}

	require.Equal(t, string(app.DisconnectReasonShutdown), ended.Reason)
	require.Len(t, baseKernel.DisconnectCalls(), 1)
}

func TestExporterShutdown_JoinsTimeoutAndStoredDisconnectFailures(t *testing.T) {
	t.Parallel()

	busIDs := [...]domain.BusID{"6-3", "6-4"}

	var decodeIndex atomic.Int32

	handoffs := make(chan struct{}, len(busIDs))
	firstFailed := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseSecond := make(chan struct{})
	secondFinished := make(chan struct{})
	firstCleanupPublished := make(chan struct{})
	logger := slog.New(&messageSignalHandler{
		message: "shutdown kernel disconnect",
		done:    firstCleanupPublished,
	})

	kernel := &ExporterKernelMock{
		ListExportedDevicesFunc: func(_ context.Context) ([]domain.Device, error) {
			return []domain.Device{{BusID: busIDs[0]}, {BusID: busIDs[1]}}, nil
		},
		ExportOnConnFunc: func(_ context.Context, _ net.Conn, _ domain.BusID) error {
			handoffs <- struct{}{}

			return nil
		},
		DisconnectFunc: func(_ context.Context, busID domain.BusID) error {
			switch busID {
			case busIDs[0]:
				close(firstFailed)

				return errFirstDisconnect
			case busIDs[1]:
				close(secondStarted)
				<-releaseSecond
				close(secondFinished)

				return errSecondDisconnect
			default:
				return nil
			}
		},
	}
	codec := newSessionImportCodec(busIDs[0])

	codec.DecodeOpReqImportBodyFunc = func(_ io.Reader) (domain.BusID, error) {
		idx := decodeIndex.Add(1) - 1

		return busIDs[idx], nil
	}

	listener := newAddrListener(&net.TCPAddr{IP: net.IPv4(10, 0, 0, 63), Port: 9063})
	exp := newExporterForTest(
		t,
		app.WithExporterKernel(kernel),
		app.WithExporterCodec(codec),
		app.WithExporterLogger(logger),
		app.WithExporterStatusPollInterval(-1),
	)

	serveDone := make(chan error, 1)
	go func() { serveDone <- exp.Serve(context.Background(), listener) }()

	clients := make([]net.Conn, 0, len(busIDs))
	for range busIDs {
		client, err := listener.dial(context.Background())
		require.NoError(t, err)

		clients = append(clients, client)
		_, err = client.Write(opHeader(wire.OpReqImport))
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		for _, client := range clients {
			_ = client.Close()
		}
	})

	for range busIDs {
		select {
		case <-handoffs:
		case <-time.After(2 * time.Second):
			t.Fatal("kernel handoff did not complete")
		}
	}

	shutdownCtx, cancelShutdown := context.WithCancel(context.Background())

	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- exp.Shutdown(shutdownCtx) }()

	select {
	case <-firstFailed:
	case <-time.After(2 * time.Second):
		t.Fatal("first Disconnect did not fail")
	}

	select {
	case <-firstCleanupPublished:
	case <-time.After(2 * time.Second):
		t.Fatal("first Disconnect failure was not published")
	}

	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second Disconnect did not block")
	}

	cancelShutdown()

	firstErr := <-shutdownDone
	require.ErrorIs(t, firstErr, context.Canceled)
	require.ErrorIs(t, firstErr, errFirstDisconnect,
		"a completed failure must not be hidden by another cleanup timeout")

	close(releaseSecond)

	select {
	case <-secondFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("second Disconnect did not finish")
	}

	repeatErr := exp.Shutdown(context.Background())
	require.ErrorIs(t, repeatErr, errFirstDisconnect)
	require.ErrorIs(t, repeatErr, errSecondDisconnect)
	require.NotErrorIs(t, repeatErr, context.Canceled)
	require.Len(t, kernel.DisconnectCalls(), len(busIDs),
		"repeat Shutdown must not repeat either kernel mutation")
	require.NoError(t, <-serveDone)
}
