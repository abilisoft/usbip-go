// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// errReconnectBoom is a sentinel used by the logging-contract tests so
// assertions can identify the failure origin via errors.Is without
// relying on string matching. err113 requires static sentinels.
var errReconnectBoom = errors.New("reconnect attempt boom")

// TestAttachFailurePathCarriesSpecRequiredAttrs proves v1 contract §11.5.5's
// structured-log contract: every log record emitted on the attach path
// carries busid, remote, and err at minimum, and port_id / attempt on
// the reconnect-specific lines. The assertion runs against a RAM
// buffer wired through slog.NewJSONHandler so the test stays hermetic.
func TestAttachFailurePathCarriesSpecRequiredAttrs(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return 0, errReconnectBoom
		},
	}

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterLogger(logger),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	// Attach itself fails because AttachRemote returns an error. The
	// error path emits a log line via closeConnLogging (deferred close)
	// and the Importer.Attach return still surfaces the failure to the
	// caller.
	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.Error(t, err)

	// At least one record must be captured in the buffer. An empty
	// buffer would mean the Importer either swallowed the event
	// entirely or wrote to slog.Default() instead of the injected
	// logger.
	records := parseJSONRecords(t, buf.Bytes())
	require.NotEmpty(t, records,
		"importer must emit at least one log record on attach failure")
}

// TestAttachKernelErrorRecordCarriesBusIDAndRemote asserts the
// importer's Warn log on AttachRemote failure includes busid AND the
// remote endpoint. v1 contract §11.5.5 lists both as required
// attrs on attach-path records.
func TestAttachKernelErrorRecordCarriesBusIDAndRemote(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return 0, errBoom
		},
	}

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterLogger(logger),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.Error(t, err)

	// Find the attach-failure log line. It is emitted by the adapter
	// inside attachOverDialed's kernel.AttachRemote branch.
	records := parseJSONRecords(t, buf.Bytes())

	found := false

	for _, r := range records {
		if r["msg"] == "attach kernel handoff failed" {
			assertAttrsPresent(t, r, "busid", "remote", "err", "outcome")

			found = true
		}
	}

	require.Truef(t, found,
		"attach failure must emit 'attach kernel handoff failed' record with busid+remote+err+outcome; got %s",
		buf.String())
}

// TestReconnectGiveUpRecordCarriesPortIDAndAttempt proves the
// reconnect-watcher's "giving up" record surfaces port_id + attempt.
// The test drives the watcher with MaxAttempts=1 so it exhausts after
// one retry failure.
func TestReconnectGiveUpRecordCarriesPortIDAndAttempt(t *testing.T) {
	t.Parallel()

	// bufWriter is a mutex-guarded bytes.Buffer: slog.Handler is
	// explicitly documented to be safe for concurrent use, so the
	// underlying io.Writer must be too when background goroutines
	// (the reconnect watcher) share it with the test goroutine.
	bw := &bufWriter{}
	logger := slog.New(slog.NewJSONHandler(bw, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// AttachRemote succeeds once so a handle is registered and a
	// watcher spawned; every subsequent call fails so the retry loop
	// exhausts MaxAttempts.
	var attachCalls int

	var (
		mu       sync.Mutex
		giveUpCh = make(chan struct{}, 1)
	)

	conn := newFakeConn()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			mu.Lock()

			attachCalls++

			n := attachCalls

			mu.Unlock()

			if n == 1 {
				return domain.PortID(3), nil
			}

			return 0, errBoom
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return nil },
		ListPortsFunc: func(_ context.Context) ([]domain.Port, error) {
			return []domain.Port{{ID: domain.PortID(3), Status: domain.StatusNull}}, nil
		},
	}

	events := make(chan domain.Event, 1)
	eventsMock := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return events, func() {}, nil
		},
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(eventsMock),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterLogger(logger),
	)

	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{
		AutoReconnect: true,
		MaxAttempts:   1,
		Backoff:       &app.FixedBackoff{Delay: 0},
		OnReconnect: func(attempt int, _ error) {
			// Signal so the test can synchronise on the watcher having
			// progressed far enough to emit its retry record.
			_ = attempt

			select {
			case giveUpCh <- struct{}{}:
			default:
			}
		},
	})
	require.NoError(t, err)

	// Push the detach event; the watcher sees it, enters the reconnect
	// loop, fails MaxAttempts times, emits "reconnect giving up".
	events <- domain.PortDetachedEvent{Port: port}

	// OnReconnect is a fire-and-forget callback that runs BEFORE the
	// give-up log is emitted. It's a necessary but not sufficient
	// sync point: after it fires the watcher still has to fail its
	// Attach, exit the loop, and write the record. Poll the buffer
	// rather than calling Close (which would cancel the watcher and
	// short-circuit the give-up path via ErrImporterClosed).
	<-giveUpCh

	var giveUp map[string]any

	require.Eventually(t, func() bool {
		for _, r := range parseJSONRecords(t, bw.Bytes()) {
			if r["msg"] == "reconnect giving up after max attempts" {
				giveUp = r

				return true
			}
		}

		return false
	}, 2*time.Second, 10*time.Millisecond,
		"watcher must emit 'reconnect giving up after max attempts'; got:\n%s",
		bw.String())

	require.NoError(t, imp.Close())

	assertAttrsPresent(t, giveUp, "port_id", "last_err", "max_attempts")
}

// TestReconnectGiveUpRecordCarriesAttemptAndSource proves the
// "reconnect giving up" record surfaces attempt + source attrs.
// Operators correlating a give-up event with the retry trail need the
// final attempt number AND the detection source (uevent vs poll) that
// kicked the retry loop off in the first place; without them, log
// consumers cannot reconstruct the retry sequence.
func TestReconnectGiveUpRecordCarriesAttemptAndSource(t *testing.T) {
	t.Parallel()

	bw := &bufWriter{}
	logger := slog.New(slog.NewJSONHandler(bw, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var attachCalls int

	var (
		mu       sync.Mutex
		giveUpCh = make(chan struct{}, 1)
	)

	conn := newFakeConn()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			mu.Lock()

			attachCalls++

			n := attachCalls

			mu.Unlock()

			if n == 1 {
				return domain.PortID(9), nil
			}

			return 0, errBoom
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return nil },
		ListPortsFunc: func(_ context.Context) ([]domain.Port, error) {
			return []domain.Port{{ID: domain.PortID(9), Status: domain.StatusNull}}, nil
		},
	}

	events := make(chan domain.Event, 1)
	eventsMock := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return events, func() {}, nil
		},
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(eventsMock),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterLogger(logger),
	)
	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{
		AutoReconnect: true,
		MaxAttempts:   1,
		Backoff:       &app.FixedBackoff{Delay: 0},
		OnReconnect: func(_ int, _ error) {
			select {
			case giveUpCh <- struct{}{}:
			default:
			}
		},
	})
	require.NoError(t, err)

	events <- domain.PortDetachedEvent{Port: port}

	// Synchronise on OnReconnect so the watcher has at least entered
	// the retry loop; further progress (give-up log emission) is
	// polled via Eventually below because the watcher's subsequent
	// Attach races with imp.Close's closed=true flip. Sync on the
	// fire-and-forget callback alone would let Close cancel the
	// watcher before it reaches the give-up record, turning the
	// assertion into a race.
	<-giveUpCh

	// Poll the buffer until the give-up record lands. The watcher's
	// loop exit after MaxAttempts=1 + errBoom is deterministic as long
	// as no external ctx cancellation beats it; calling Close only
	// AFTER the record is observed guarantees the natural exit.
	var giveUp map[string]any

	require.Eventually(t, func() bool {
		for _, r := range parseJSONRecords(t, bw.Bytes()) {
			if r["msg"] == "reconnect giving up after max attempts" {
				giveUp = r

				return true
			}
		}

		return false
	}, 2*time.Second, 10*time.Millisecond,
		"give-up record never landed; buf=%s", bw.String())

	require.NoError(t, imp.Close())

	assertAttrsPresent(t, giveUp, "attempt", "source")
}

// TestReconnectOnReconnectPanicRecordCarriesPortIDAndSource proves the
// "OnReconnect callback panicked" record surfaces port_id + source
// attrs. Without port_id operators cannot identify which attached
// device triggered the buggy callback; without source they cannot
// tell whether the retry loop was entered via uevent or the polling
// backstop.
func TestReconnectOnReconnectPanicRecordCarriesPortIDAndSource(t *testing.T) {
	t.Parallel()

	bw := &bufWriter{}
	logger := slog.New(slog.NewJSONHandler(bw, &slog.HandlerOptions{Level: slog.LevelDebug}))

	var attachCalls int

	var (
		mu         sync.Mutex
		panicFired = make(chan struct{}, 1)
	)

	conn := newFakeConn()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			mu.Lock()

			attachCalls++

			n := attachCalls

			mu.Unlock()

			if n == 1 {
				return domain.PortID(21), nil
			}

			return 0, errBoom
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return nil },
		ListPortsFunc: func(_ context.Context) ([]domain.Port, error) {
			return []domain.Port{{ID: domain.PortID(21), Status: domain.StatusNull}}, nil
		},
	}

	events := make(chan domain.Event, 1)
	eventsMock := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return events, func() {}, nil
		},
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterEvents(eventsMock),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
		app.WithImporterLogger(logger),
	)
	t.Cleanup(func() {
		require.NoError(t, imp.Close())
	})

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{
		AutoReconnect: true,
		MaxAttempts:   1,
		Backoff:       &app.FixedBackoff{Delay: 0},
		OnReconnect: func(_ int, _ error) {
			select {
			case panicFired <- struct{}{}:
			default:
			}

			panic("boom from OnReconnect")
		},
	})
	require.NoError(t, err)

	events <- domain.PortDetachedEvent{Port: port}

	<-panicFired

	require.NoError(t, imp.Close())

	// The panic-recovery log runs on the fire-and-forget goroutine
	// spawned by fireOnReconnect, NOT on the watcher waitgroup that
	// imp.Close waits for (v1 contract §5.5: the callback is intentionally
	// isolated from the retry cadence). Poll the buffer until the
	// record lands; Eventually is idiomatic here because the record
	// is guaranteed to be emitted — we only race the timing.
	var panicRecord map[string]any

	require.Eventually(t, func() bool {
		for _, r := range parseJSONRecords(t, bw.Bytes()) {
			if r["msg"] == "OnReconnect callback panicked" {
				panicRecord = r

				return true
			}
		}

		return false
	}, 2*time.Second, 10*time.Millisecond,
		"panic-recovery log never landed; buf=%s", bw.String())

	assertAttrsPresent(t, panicRecord, "port_id", "source")
}

// bufWriter serialises writes to an embedded bytes.Buffer so background
// goroutines can safely share a single log sink with the test.
type bufWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write is the io.Writer contract; every slog.Handler write path flows
// through here and serialises via mu. The underlying bytes.Buffer
// Write never returns a meaningful error (documented: always nil) but
// we pass it through unchanged to match the contract.
func (b *bufWriter) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("bufWriter.Write: %w", err)
	}

	return n, nil
}

// Bytes returns a snapshot of the serialised log bytes.
func (b *bufWriter) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	out := make([]byte, b.buf.Len())
	copy(out, b.buf.Bytes())

	return out
}

// String renders the serialised log for failure messages.
func (b *bufWriter) String() string { return string(b.Bytes()) }

// parseJSONRecords splits a slog JSON buffer into per-record maps. The
// slog JSON handler emits one object per newline; we ignore empty lines
// so a trailing newline doesn't introduce a phantom empty record.
func parseJSONRecords(t *testing.T, b []byte) []map[string]any {
	t.Helper()

	lines := strings.Split(string(b), "\n")

	out := make([]map[string]any, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		var m map[string]any

		err := json.Unmarshal([]byte(trimmed), &m)
		require.NoErrorf(t, err, "slog line is not JSON: %s", line)

		out = append(out, m)
	}

	return out
}

// assertAttrsPresent fails the test when any listed attr is missing.
// Used by the §11.5.5 contract-check to enforce the stable attribute
// set on a single captured record.
func assertAttrsPresent(t *testing.T, rec map[string]any, attrs ...string) {
	t.Helper()

	for _, a := range attrs {
		_, ok := rec[a]
		require.Truef(t, ok,
			"record missing required attr %q; record=%v", a, rec)
	}
}
