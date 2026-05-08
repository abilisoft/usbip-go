package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// drainTestServer spins a minimal status UDS answering GET / with a
// provided sessionsFn (so tests can model "first non-empty, then
// empty") and POST /drain by incrementing drainCalls. Returns the
// UDS path + a cleanup that shuts the server and unlinks the file.
type drainTestServer struct {
	drainCalls atomic.Int32
	server     *http.Server
	listener   net.Listener
}

type drainTestState struct {
	// accepting is the listening.accepting flag reported by GET /; the
	// drain client treats accepting=false AND sessions=[] as success.
	accepting bool
	// sessions is the JSON-rendered sessions array.
	sessions []any
}

func newDrainTestServer(
	t *testing.T,
	sockPath string,
	stateFn func(drainCalled int) drainTestState,
) *drainTestServer {
	t.Helper()

	var lc net.ListenConfig

	lis, err := lc.Listen(context.Background(), "unix", sockPath)
	require.NoError(t, err)

	s := &drainTestServer{listener: lis}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		state := stateFn(int(s.drainCalls.Load()))

		body := map[string]any{
			"schema":        "v1",
			"version":       "test",
			"commit":        "none",
			"uptime_sec":    1,
			"listening":     map[string]any{"addr": "", "activation": false, "accepting": state.accepting},
			"bound_devices": []any{},
			"sessions":      state.sessions,
			"kernel_modules": map[string]any{
				"usbip_core": "loaded",
				"vhci_hcd":   "loaded",
				"usbip_host": "loaded",
			},
		}

		w.Header().Set("Content-Type", "application/json")

		enc := json.NewEncoder(w)

		encErr := enc.Encode(body)
		if encErr != nil {
			// require inside the handler goroutine would call t.Fatal
			// off-thread; record the failure via t.Errorf instead and
			// let the test observe the missing body through the http
			// client seeing an early EOF.
			t.Errorf("drain test server: encode: %v", encErr)
		}
	})

	mux.HandleFunc("POST /drain", func(w http.ResponseWriter, _ *http.Request) {
		s.drainCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}

	go func() {
		_ = s.server.Serve(lis)
	}()

	return s
}

func (s *drainTestServer) Close() {
	_ = s.server.Close()
	_ = s.listener.Close()
}

// TestDrainSubcommandSuccess drives the happy path: the server
// initially reports one session; after POST /drain the session list
// empties and accepting flips to false. drain should exit 0.
func TestDrainSubcommandSuccess(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	srv := newDrainTestServer(t, sockPath, func(drainCalled int) drainTestState {
		if drainCalled == 0 {
			return drainTestState{
				accepting: true,
				sessions:  []any{map[string]any{"id": "abc"}},
			}
		}

		return drainTestState{accepting: false, sessions: []any{}}
	})
	t.Cleanup(srv.Close)

	cmd := newDrainCmd()
	cmd.SetArgs([]string{
		"--status-socket", sockPath,
		"--drain-timeout", "3s",
		"--poll-interval", "20ms",
	})

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	require.NoError(t, err, "stderr: %s", stderr.String())

	require.EqualValues(t, 1, srv.drainCalls.Load(),
		"drain should have POSTed /drain exactly once")
}

// TestDrainSubcommandTimeout proves drain exits with errDrainTimeout
// when the server never reports sessions=[] within --drain-timeout.
func TestDrainSubcommandTimeout(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	srv := newDrainTestServer(t, sockPath, func(_ int) drainTestState {
		return drainTestState{
			accepting: true,
			sessions:  []any{map[string]any{"id": "stuck"}},
		}
	})
	t.Cleanup(srv.Close)

	cmd := newDrainCmd()
	cmd.SetArgs([]string{
		"--status-socket", sockPath,
		"--drain-timeout", "200ms",
		"--poll-interval", "20ms",
	})

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, errDrainTimeout)
	require.Contains(t, stderr.String(), "drain timed out")
}

// TestDrainSubcommandUDSDisappears proves the client treats the daemon
// exiting during drain (UDS gone) as success: a closed connection
// after the first POST should produce exit 0 without a timeout wait.
func TestDrainSubcommandUDSDisappears(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	srv := newDrainTestServer(t, sockPath, func(_ int) drainTestState {
		return drainTestState{
			accepting: true,
			sessions:  []any{map[string]any{"id": "abc"}},
		}
	})

	// Close the server shortly after the first POST /drain so the
	// polling client observes a dial failure → success per spec §7.7.
	go func() {
		// Wait for at least one drain call.
		for srv.drainCalls.Load() == 0 {
			time.Sleep(10 * time.Millisecond)
		}

		srv.Close()
	}()

	cmd := newDrainCmd()
	cmd.SetArgs([]string{
		"--status-socket", sockPath,
		"--drain-timeout", "3s",
		"--poll-interval", "20ms",
	})

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	require.NoError(t, err, "stderr: %s", stderr.String())
}
