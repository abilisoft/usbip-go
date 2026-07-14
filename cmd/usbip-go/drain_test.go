// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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

// roundTripFunc adapts a function to http.RoundTripper so probe validation
// tests can supply exact status codes and JSON bodies without a network
// listener or timing-sensitive server goroutine.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// drainTestServer spins a minimal status UDS answering GET / with a
// provided sessionsFn (so tests can model "first non-empty, then
// empty") and POST /drain by incrementing drainCalls. Returns the
// UDS path + a cleanup that shuts the server and unlinks the file.
type drainTestServer struct {
	drainCalls              atomic.Int32
	closeListenerAfterDrain atomic.Bool
	server                  *http.Server
	listener                net.Listener
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
			"schema":         "v1",
			testVersionToken: "test",
			"commit":         "none",
			"uptime_sec":     1,
			"listening":      map[string]any{"addr": "", "activation": false, "accepting": state.accepting},
			"bound_devices":  []any{},
			"sessions":       state.sessions,
			"kernel_modules": map[string]any{
				testUSBIPCoreModule: testLoadedModuleState,
				testVHCIHCDModule:   testLoadedModuleState,
				testUSBIPHostModule: testLoadedModuleState,
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
		// Force the subsequent status probe onto a new UDS connection. The
		// daemon-disappears scenario closes the listener after this response;
		// reusing the POST connection would nondeterministically surface
		// ECONNRESET even though the classifier intentionally accepts only
		// dial-time ENOENT/ECONNREFUSED as proof that the daemon exited.
		w.Header().Set("Connection", "close")
		w.WriteHeader(http.StatusOK)

		flushErr := http.NewResponseController(w).Flush()
		if flushErr != nil {
			t.Errorf("drain test server: flush POST response: %v", flushErr)
		}

		if s.closeListenerAfterDrain.Load() {
			closeErr := s.listener.Close()
			if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				t.Errorf("drain test server: close listener after POST: %v", closeErr)
			}
		}
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

	dir := shortSocketTempDir(t)
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
		testStatusSocketFlag, sockPath,
		testDrainTimeoutFlag, "3s",
		testPollIntervalFlag, testPollIntervalValue,
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

	dir := shortSocketTempDir(t)
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
		testStatusSocketFlag, sockPath,
		testDrainTimeoutFlag, "200ms",
		testPollIntervalFlag, testPollIntervalValue,
	})

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, errDrainTimeout)
	// runDrain returns the timeout error; main's renderMainError is
	// the sole stderr writer for it (so the message is not printed
	// twice). cobra-internal stderr stays silent because newDrainCmd
	// sets SilenceErrors=true. The drain-timeout phrase MUST appear
	// in the returned error itself so renderMainError can format it.
	require.Contains(t, err.Error(), "drain timed out")
}

// TestProbeStatusOnceValidatesSchemaAndRequiredFields pins the fail-closed
// drain contract. JSON zero values are not evidence that a daemon is idle:
// GET must be 2xx schema v1 and explicitly carry sessions plus
// listening.accepting before the client can make a completion decision.
func TestProbeStatusOnceValidatesSchemaAndRequiredFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		body       string
		wantDone   bool
		wantErr    bool
	}{
		{
			name:       "valid idle status",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","sessions":[],"listening":{"accepting":false}}`,
			wantDone:   true,
		},
		{
			name:       "valid active status",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","sessions":[{}],"listening":{"accepting":false}}`,
		},
		{
			name:       "valid accepting status",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","sessions":[],"listening":{"accepting":true}}`,
		},
		{
			name:       "additive fields ignored",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","future":true,"sessions":[],"listening":{"addr":"x","accepting":false}}`,
			wantDone:   true,
		},
		{
			name:       "non-2xx",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"schema":"v1","sessions":[],"listening":{"accepting":false}}`,
			wantErr:    true,
		},
		{
			name:       "empty object",
			statusCode: http.StatusOK,
			body:       `{}`,
			wantErr:    true,
		},
		{
			name:       "wrong schema",
			statusCode: http.StatusOK,
			body:       `{"schema":"v2","sessions":[],"listening":{"accepting":false}}`,
			wantErr:    true,
		},
		{
			name:       "missing sessions",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","listening":{"accepting":false}}`,
			wantErr:    true,
		},
		{
			name:       "null sessions",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","sessions":null,"listening":{"accepting":false}}`,
			wantErr:    true,
		},
		{
			name:       "missing listening",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","sessions":[]}`,
			wantErr:    true,
		},
		{
			name:       "missing accepting",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","sessions":[],"listening":{}}`,
			wantErr:    true,
		},
		{
			name:       "null accepting",
			statusCode: http.StatusOK,
			body:       `{"schema":"v1","sessions":[],"listening":{"accepting":null}}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: tt.statusCode,
					Body:       io.NopCloser(bytes.NewBufferString(tt.body)),
					Header:     make(http.Header),
				}, nil
			})}

			done, err := probeStatusOnce(t.Context(), client)
			if tt.wantErr {
				require.Error(t, err)
				require.False(t, done)

				return
			}

			require.NoError(t, err)
			require.Equal(t, tt.wantDone, done)
		})
	}
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

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	srv := newDrainTestServer(t, sockPath, func(_ int) drainTestState {
		return drainTestState{
			accepting: true,
			sessions:  []any{map[string]any{"id": "abc"}},
		}
	})

	// Close the listener synchronously after flushing POST /drain so the next
	// status probe must dial and observe daemon disappearance. Closing from a
	// peer goroutine races the immediate probe and can reset a newly accepted
	// connection, which is intentionally not classified as proof of exit.
	srv.closeListenerAfterDrain.Store(true)

	cmd := newDrainCmd()
	cmd.SetArgs([]string{
		testStatusSocketFlag, sockPath,
		testDrainTimeoutFlag, "3s",
		testPollIntervalFlag, testPollIntervalValue,
	})

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	require.NoError(t, err, "stderr: %s", stderr.String())
}

// TestDrainSubcommandRejectsNonPositivePollInterval pins the
// operator-ergonomics guard: time.NewTicker(0) panics, time.NewTicker
// of a negative duration also panics. A real operator can crash the
// drain client with `usbip drain --poll-interval=0s` if the value
// reaches NewTicker unvalidated. Catch it at the cobra layer with a
// targeted error mapped to ExitUsage.
func TestDrainSubcommandRejectsNonPositivePollInterval(t *testing.T) {
	t.Parallel()

	for _, badInterval := range []string{"0s", "-1s"} {
		t.Run(badInterval, func(t *testing.T) {
			t.Parallel()

			cmd := newDrainCmd()
			cmd.SetArgs([]string{
				testStatusSocketFlag, "/run/usbip-go/status.sock",
				testPollIntervalFlag, badInterval,
			})

			var stdout, stderr bytes.Buffer

			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)

			err := cmd.ExecuteContext(context.Background())
			require.Errorf(t, err, "drain must reject non-positive --poll-interval %q", badInterval)
			require.Contains(t, err.Error(), "poll-interval",
				"error must name the offending flag so operators can correct it")
			require.Equalf(t, ExitUsage, MapError(err),
				"non-positive --poll-interval is a usage class fault and must map to ExitUsage")
		})
	}
}

// TestDrainSubcommandRejectsEmptyStatusSocket pins the operator-
// ergonomics fix: when --status-socket is empty (status endpoint
// disabled on the daemon side), `usbip drain` MUST surface a
// targeted error explaining the disabled state rather than fall
// through to a raw dial failure with no context. The error MUST
// also map to ExitUsage (2), not ExitGeneric (1), because empty
// --status-socket is a configuration / preflight class fault.
func TestDrainSubcommandRejectsEmptyStatusSocket(t *testing.T) {
	t.Parallel()

	cmd := newDrainCmd()
	cmd.SetArgs([]string{testStatusSocketFlag, ""})

	var stdout, stderr bytes.Buffer

	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.ExecuteContext(context.Background())
	require.Error(t, err, "drain must reject an empty status socket path")
	require.Contains(t, err.Error(), "status socket",
		"error message must name the disabled mechanism so operators understand the root cause")
	require.Equalf(t, ExitUsage, MapError(err),
		"empty --status-socket is a configuration class fault and must map to ExitUsage (2), got %d",
		MapError(err))
}
