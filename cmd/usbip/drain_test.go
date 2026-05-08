// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
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

// TestIsDaemonGoneErrorClassification pins the transport-error
// narrowing for drain: a dial failure counts as "daemon gone" only
// when the UDS is truly gone (ENOENT / ErrNotExist) or the peer
// refused the connection (ECONNREFUSED). Any other *net.OpError —
// including EACCES on bind, ETIMEDOUT on dial, connection reset, or
// generic i/o errors — must propagate so operators can tell "daemon
// drained" from "transport wedged".
func TestIsDaemonGoneErrorClassification(t *testing.T) {
	t.Parallel()

	// Synthetic *net.OpError carriers so we can drive the classifier
	// with specific wrapped syscall errnos. DialContext-style OpErrors
	// wrap the socket.connect errno under .Err, which errors.Is walks.
	opErrWith := func(inner error) error {
		return &net.OpError{
			Op:   "dial",
			Net:  "unix",
			Addr: &net.UnixAddr{Name: "/tmp/does-not-matter.sock", Net: "unix"},
			Err:  inner,
		}
	}

	// Daemon-gone paths. Every one of these MUST classify as drained.
	require.True(t, isDaemonGoneError(net.ErrClosed),
		"net.ErrClosed → daemon gone")
	require.True(t, isDaemonGoneError(opErrWith(syscall.ECONNREFUSED)),
		"ECONNREFUSED → daemon gone (peer exited between polls)")
	require.True(t, isDaemonGoneError(opErrWith(&os.PathError{
		Op: "connect", Path: "/tmp/does-not-matter.sock", Err: syscall.ENOENT,
	})), "ENOENT under OpError → daemon gone (socket unlinked)")
	require.True(t, isDaemonGoneError(opErrWith(fs.ErrNotExist)),
		"fs.ErrNotExist under OpError → daemon gone")

	// Non-gone paths. Every one of these MUST propagate — the legacy
	// "any *net.OpError counts as drained" heuristic mishandled them.
	require.False(t, isDaemonGoneError(opErrWith(syscall.EACCES)),
		"EACCES under OpError → transport error, not drained")
	require.False(t, isDaemonGoneError(opErrWith(syscall.ETIMEDOUT)),
		"ETIMEDOUT under OpError → transport error, not drained")
	require.False(t, isDaemonGoneError(opErrWith(syscall.ECONNRESET)),
		"ECONNRESET under OpError → transport error, not drained")
	require.False(t, isDaemonGoneError(opErrWith(errWeirdIOFail)),
		"arbitrary net.OpError → transport error, not drained")
	require.False(t, isDaemonGoneError(errBareNonNet),
		"bare error → not drained")
	require.False(t, isDaemonGoneError(nil), "nil → not drained")
}

// errWeirdIOFail + errBareNonNet are test-scoped sentinels used to
// exercise isDaemonGoneError with non-recognised errors; err113 wants
// static errors for fixtures.
var (
	errWeirdIOFail = errors.New("weird io fail")
	errBareNonNet  = errors.New("bare non-net error")
)

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
	// polling client observes a dial failure → success per v1 contract §7.7.
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

// TestDrainSubcommandRejectsEmptyStatusSocket pins the operator-
// ergonomics fix: when --status-socket is empty (status endpoint
// disabled on the daemon side), `usbip drain` MUST surface a
// targeted error explaining the disabled state rather than fall
// through to a raw dial failure with no context.
func TestDrainSubcommandRejectsEmptyStatusSocket(t *testing.T) {
	t.Parallel()

	cmd := newDrainCmd()
	cmd.SetArgs([]string{"--status-socket", ""})

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	require.Error(t, err, "drain must reject an empty status socket path")
	require.Contains(t, err.Error(), "status socket",
		"error message must name the disabled mechanism so operators understand the root cause")
}
