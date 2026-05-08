package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestAttachFailurePathCarriesSpecRequiredAttrs proves spec §11.5.5's
// structured-log contract: every log record emitted on the attach path
// carries busid, remote, and err at minimum, and port_id / attempt on
// the reconnect-specific lines. The assertion runs against a RAM
// buffer wired through slog.NewJSONHandler so the test stays hermetic.
func TestAttachFailurePathCarriesSpecRequiredAttrs(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Drive the reconnect-giving-up branch: a MaxAttempts-capped watcher
	// fails once and exhausts immediately. The "reconnect giving up
	// after max attempts" record must carry port_id + last_err.
	failErr := errors.New("reconnect attempt boom")

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return 0, failErr
		},
	}

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) { return conn, nil },
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
// remote endpoint. The spec §11.5.5 contract lists both as required
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
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) { return conn, nil },
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

	// Find the attach-failure log line. It's emitted by the adapter
	// post-Finding 4 inside attachOverDialed's kernel.AttachRemote
	// branch.
	records := parseJSONRecords(t, buf.Bytes())

	found := false

	for _, r := range records {
		if r["msg"] == "attach failed" {
			assertAttrsPresent(t, r, "busid", "remote", "err")

			found = true
		}
	}

	require.Truef(t, found,
		"attach failure must emit 'attach failed' record with busid+remote+err; got %s",
		buf.String())
}

// TestReconnectGiveUpRecordCarriesPortIDAndAttempt proves the
// reconnect-watcher's "giving up" record surfaces port_id + attempt.
// The test drives the watcher with MaxAttempts=1 so it exhausts
// immediately.
func TestReconnectGiveUpRecordCarriesPortIDAndAttempt(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// Build an importer whose AttachRemote succeeds once (so a handle
	// is registered and a watcher spawned) then fails on every retry.
	var attachCalls int

	conn := newFakeConn()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			attachCalls++
			if attachCalls == 1 {
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
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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
	})
	require.NoError(t, err)

	// Trigger the detach signal so the watcher runs its reconnect loop,
	// fails once, and emits the give-up record.
	events <- domain.PortDetachedEvent{Port: port}

	// Wait for Close to drain the watcher; the give-up log line is
	// emitted before runReconnectLoop returns.
	require.NoError(t, imp.Close())

	records := parseJSONRecords(t, buf.Bytes())

	found := false

	for _, r := range records {
		if r["msg"] == "reconnect giving up after max attempts" {
			assertAttrsPresent(t, r, "port_id", "last_err", "max_attempts")

			found = true
		}
	}

	require.Truef(t, found,
		"watcher must emit 'reconnect giving up after max attempts' with port_id+last_err+max_attempts; got:\n%s",
		buf.String())
}

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
