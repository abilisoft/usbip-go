// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// fakeStatusSource is the substitutable seam the status server queries
// for exporter state. Production wiring uses *statusExporter which
// wraps a *usbip.Exporter; tests provide a stub so the handler is
// exercisable without a real Linux kernel.
type fakeStatusSource struct {
	bound      []usbip.Device
	sessions   []usbip.Session
	accepting  bool
	listenAddr string
	activation bool
	modules    map[string]usbip.ModuleState

	drainCalled atomic.Int32
	drainErr    error
	modulesErr  error
	mu          sync.Mutex
}

// blockingStatusSource holds a GET handler in BoundDevices until its request
// context is canceled. It lets the shutdown regression prove serveStatus
// does not return while an accepted active connection or handler survives.
type blockingStatusSource struct {
	*fakeStatusSource

	entered  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func (s *blockingStatusSource) BoundDevices(ctx context.Context) ([]usbip.Device, error) {
	s.once.Do(func() { close(s.entered) })
	<-ctx.Done()
	close(s.canceled)

	return nil, fmt.Errorf("status request canceled: %w", ctx.Err())
}

func (f *fakeStatusSource) BoundDevices(_ context.Context) ([]usbip.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]usbip.Device{}, f.bound...), nil
}

func (f *fakeStatusSource) Sessions(_ context.Context) []usbip.Session {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]usbip.Session{}, f.sessions...)
}

func (f *fakeStatusSource) Listening() listeningState {
	f.mu.Lock()
	defer f.mu.Unlock()

	return listeningState{
		Addr:       f.listenAddr,
		Activation: f.activation,
		Accepting:  f.accepting,
	}
}

func (f *fakeStatusSource) KernelModules(_ context.Context) (map[string]usbip.ModuleState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]usbip.ModuleState, len(f.modules))
	maps.Copy(out, f.modules)

	return out, f.modulesErr
}

func (f *fakeStatusSource) Drain(_ context.Context) error {
	f.drainCalled.Add(1)

	return f.drainErr
}

// udsHTTPClientTimeout caps every helper HTTP call so a broken server
// can't wedge the test suite.
const udsHTTPClientTimeout = 5 * time.Second

// shortSocketTempDir avoids Linux's 108-byte sun_path ceiling when tests use a
// deliberately persistent, long GOTMPDIR. testing.T.TempDir includes the full
// test name and can make an otherwise valid UDS test fail with EINVAL.
func shortSocketTempDir(t *testing.T) string {
	t.Helper()

	root := os.Getenv("GOTMPDIR")
	if root == "" {
		root = os.TempDir()
	}

	rootDir, err := os.OpenRoot(root)
	require.NoError(t, err)

	name := "u-" + rand.Text()
	require.NoError(t, rootDir.Mkdir(name, 0o700))

	t.Cleanup(func() {
		socketDir, openErr := rootDir.OpenRoot(name)
		require.NoError(t, openErr)

		for _, basename := range []string{"status.sock", "status.sock.lock"} {
			removeErr := socketDir.Remove(basename)
			require.True(t, removeErr == nil || errors.Is(removeErr, fs.ErrNotExist))
		}

		require.NoError(t, socketDir.Close())
		require.NoError(t, rootDir.Remove(name))
		require.NoError(t, rootDir.Close())
	})

	return filepath.Join(root, name)
}

// newUDSHTTPClient returns an http.Client that dials the given UDS
// path. Every request uses scheme http with the host ignored by the
// transport; the URL must still be valid.
func newUDSHTTPClient(path string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer

				return d.DialContext(ctx, "unix", path)
			},
		},
		Timeout: udsHTTPClientTimeout,
	}
}

// startStatusTestServer spins a status server in a goroutine backed by
// the given source and returns the UDS path + a cleanup function.
func startStatusTestServer(t *testing.T, src statusSource, sockPath string) func() {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		err := serveStatus(ctx, sockPath, "", src, started)
		if err != nil && !isClosedErr(err) {
			t.Logf("serveStatus exited: %v", err)
		}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("status server did not start within 2s")
	}

	return func() {
		cancel()
		<-done
	}
}

// isClosedErr suppresses the expected "use of closed network
// connection" race that fires when ctx cancels Serve while Accept is
// parked in the goroutine.
func isClosedErr(err error) bool {
	if err == nil {
		return false
	}

	// net.ErrClosed is the sentinel; errors from the unix-listener's
	// accept loop wrap it.
	if errors.Is(err, net.ErrClosed) {
		return true
	}

	return err.Error() == "accept unix: use of closed network connection"
}

// TestStatusUnlinkStale proves a stale socket file (nobody listening)
// is unlinked and recreated instead of blocking startup.
func TestStatusUnlinkStale(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	// Leave a stale file at sockPath with no listener on it. Path is
	// rooted at a test-owned temporary directory, so gosec G304's
	// "variable inclusion" is a
	// false positive here — the filename is test-controlled, not
	// attacker-derived.
	f, err := os.Create(filepath.Clean(sockPath))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	src := &fakeStatusSource{
		listenAddr: defaultListen,
		accepting:  true,
		modules: map[string]usbip.ModuleState{
			testUSBIPCoreModule: usbip.ModuleStateLoaded,
			testVHCIHCDModule:   usbip.ModuleStateLoaded,
			testUSBIPHostModule: usbip.ModuleStateLoaded,
		},
	}

	cleanup := startStatusTestServer(t, src, sockPath)
	t.Cleanup(cleanup)

	// Verify the UDS responds.
	client := newUDSHTTPClient(sockPath)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://usbipd/", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestStatusAlreadyRunning proves that a live socket (another
// usbipd-ish process answering on it) causes the second startup to
// return errAlreadyRunning.
func TestStatusAlreadyRunning(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	// Spin a plain unix listener at sockPath to simulate "another
	// usbipd is here". Any dial succeeding signals already-running.
	var lc net.ListenConfig

	lis, err := lc.Listen(context.Background(), "unix", sockPath)
	require.NoError(t, err)

	t.Cleanup(func() { _ = lis.Close() })

	// Accept and immediately close any connection so the dial
	// succeeds but no data is served. The status server's detect
	// path only cares that the dial itself succeeds.
	go func() {
		for {
			c, aerr := lis.Accept()
			if aerr != nil {
				return
			}

			_ = c.Close()
		}
	}()

	err = detectAlreadyRunning(context.Background(), sockPath)
	require.ErrorIs(t, err, errAlreadyRunning)
}

// TestServeStatusCancelsActiveHandlersBeforeReturn proves listener shutdown
// also owns already-accepted HTTP connections. The active GET blocks in its
// source until the handler context is canceled; serveStatus may return only
// after that cancellation has happened and the client connection has closed.
func TestServeStatusCancelsActiveHandlersBeforeReturn(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &blockingStatusSource{
		fakeStatusSource: &fakeStatusSource{
			modules: map[string]usbip.ModuleState{},
		},
		entered:  make(chan struct{}),
		canceled: make(chan struct{}),
	}

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	serverDone := make(chan error, 1)

	go func() {
		serverDone <- serveStatus(ctx, sockPath, "", src, started)
	}()

	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("status server did not start")
	}

	client := newUDSHTTPClient(sockPath)
	t.Cleanup(client.CloseIdleConnections)

	reqCtx, cancelRequest := context.WithCancel(t.Context())
	t.Cleanup(cancelRequest)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, "http://usbipd/", nil)
	require.NoError(t, err)

	requestDone := make(chan error, 1)

	go func() {
		resp, doErr := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}

		requestDone <- doErr
	}()

	select {
	case <-src.entered:
	case <-t.Context().Done():
		t.Fatal("status GET handler did not enter BoundDevices")
	}

	cancel()

	select {
	case serveErr := <-serverDone:
		require.NoError(t, serveErr)
	case <-t.Context().Done():
		t.Fatal("serveStatus did not return after cancellation")
	}

	select {
	case <-src.canceled:
	default:
		t.Fatal("serveStatus returned before canceling the active handler context")
	}

	select {
	case <-requestDone:
	case <-t.Context().Done():
		t.Fatal("active status client connection remained open after serveStatus returned")
	}
}

// TestServeStatusClosesIdleAcceptedConnections proves graceful status-server
// shutdown owns keep-alive connections after their request handler has
// returned. The raw UDS connection remains idle after a complete GET; server
// cancellation must close it before serveStatus reports completion.
func TestServeStatusClosesIdleAcceptedConnections(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{modules: map[string]usbip.ModuleState{}}
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	serverDone := make(chan error, 1)

	go func() {
		serverDone <- serveStatus(ctx, sockPath, "", src, started)
	}()

	select {
	case <-started:
	case <-t.Context().Done():
		t.Fatal("status server did not start")
	}

	var dialer net.Dialer

	conn, err := dialer.DialContext(t.Context(), "unix", sockPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: usbipd\r\n\r\n")
	require.NoError(t, err)

	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	cancel()

	select {
	case serveErr := <-serverDone:
		require.NoError(t, serveErr)
	case <-t.Context().Done():
		t.Fatal("serveStatus did not return after cancellation")
	}

	oneByte := make([]byte, 1)

	_, err = conn.Read(oneByte)
	require.ErrorIs(t, err, io.EOF,
		"serveStatus returned while an accepted idle connection remained open")
}

// TestCloseStatusServerForceClosesUncooperativeHandler exercises the bounded
// fallback when a handler does not observe its context. The injected zero
// timeout avoids sleeps: Shutdown reaches its deadline and Close tears down the
// accepted connection before the helper returns.
func TestCloseStatusServerForceClosesUncooperativeHandler(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	release := make(chan struct{})
	handlerDone := make(chan struct{})

	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		close(entered)
		<-release
		close(handlerDone)
	}))
	server.Start()

	requestDone := make(chan error, 1)

	go func() {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
		if err != nil {
			requestDone <- err

			return
		}

		resp, err := server.Client().Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}

		requestDone <- err
	}()

	select {
	case <-entered:
	case <-t.Context().Done():
		t.Fatal("test handler was not accepted")
	}

	err := closeStatusServerWithTimeout(t.Context(), server.Config, 0)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	close(release)

	select {
	case <-handlerDone:
	case <-t.Context().Done():
		t.Fatal("test handler did not return after release")
	}

	select {
	case requestErr := <-requestDone:
		require.Error(t, requestErr,
			"forced status-server close must terminate the accepted request")
	case <-t.Context().Done():
		t.Fatal("status client connection remained open after forced close")
	}

	server.Close()
}

// TestCombineStatusServerErrors pins expected-close filtering and preserves
// both independent failures when Serve and the close pass fail together.
func TestCombineStatusServerErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		serveErr  error
		closeErr  error
		wantErr   bool
		wantServe bool
		wantClose bool
	}{
		{name: "clean"},
		{name: "expected serve close", serveErr: net.ErrClosed},
		{name: "expected server close", closeErr: http.ErrServerClosed},
		{name: "serve failure", serveErr: errTest, wantErr: true, wantServe: true},
		{name: "close failure", closeErr: errTest, wantErr: true, wantClose: true},
		{
			name:      "joined failures",
			serveErr:  errTest,
			closeErr:  errTest,
			wantErr:   true,
			wantServe: true,
			wantClose: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := combineStatusServerErrors(tt.serveErr, tt.closeErr)
			if !tt.wantErr {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, errTest)
			require.Equal(t, tt.wantServe, strings.Contains(err.Error(), "serve status"))
			require.Equal(t, tt.wantClose, strings.Contains(err.Error(), "close status server"))
		})
	}
}

// TestStatusGetJSON proves the operations-observability and json-contracts
// OpenSpec v1 schema is rendered by GET /. Every required key is asserted.
func TestStatusGetJSON(t *testing.T) {
	t.Parallel()

	sessionID := domain.SessionID{
		0x01, 0x02, 0x03, 0x04,
		0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c,
		0x0d, 0x0e, 0x0f, 0x10,
	}

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: defaultListen,
		activation: true,
		accepting:  true,
		bound: []usbip.Device{
			{BusID: usbip.BusID("1-1.2"), VendorID: 0x0951, ProductID: 0x1666},
		},
		sessions: []usbip.Session{
			{
				ID:         sessionID,
				BusID:      usbip.BusID("1-1.2"),
				RemoteAddr: netip.MustParseAddrPort("10.0.0.8:54221"),
				StartedAt:  time.Unix(1_700_000_000, 0),
			},
		},
		modules: map[string]usbip.ModuleState{
			testUSBIPCoreModule: usbip.ModuleStateLoaded,
			testVHCIHCDModule:   usbip.ModuleStateLoaded,
			testUSBIPHostModule: usbip.ModuleStateMissing,
		},
	}

	cleanup := startStatusTestServer(t, src, sockPath)
	t.Cleanup(cleanup)

	client := newUDSHTTPClient(sockPath)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://usbipd/", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var decoded map[string]any

	require.NoError(t, json.Unmarshal(body, &decoded))

	require.Equal(t, "v1", decoded["schema"])
	require.Contains(t, decoded, "version")
	require.Contains(t, decoded, "commit")
	require.Contains(t, decoded, "uptime_sec")
	require.Contains(t, decoded, "listening")
	require.Contains(t, decoded, "bound_devices")
	require.Contains(t, decoded, "sessions")
	require.Contains(t, decoded, "kernel_modules")

	km, ok := decoded["kernel_modules"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, testLoadedModuleState, km[testUSBIPCoreModule])
	require.Equal(t, testLoadedModuleState, km[testVHCIHCDModule])
	require.Equal(t, "missing", km[testUSBIPHostModule])
}

// TestStatusDrainTriggersShutdown proves POST /drain invokes Drain on
// the underlying source and returns 200 immediately.
func TestStatusDrainTriggersShutdown(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: defaultListen,
		accepting:  true,
		modules:    map[string]usbip.ModuleState{testUSBIPCoreModule: usbip.ModuleStateLoaded},
	}

	cleanup := startStatusTestServer(t, src, sockPath)
	t.Cleanup(cleanup)

	client := newUDSHTTPClient(sockPath)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://usbipd/drain", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusAccepted, resp.StatusCode,
		"first POST /drain returns 202 Accepted; idempotent repeats return 200")

	// Drain runs in a goroutine; give it up to 500ms to be invoked.
	require.Eventually(t, func() bool {
		return src.drainCalled.Load() == 1
	}, 500*time.Millisecond, 10*time.Millisecond)
}

// TestStatusFileMode0660 verifies the UDS is created with mode 0660.
// Group chown is best-effort; we check that if the "usbip" group
// exists, the bind succeeds (a stricter chown-applied assertion would
// require root to be meaningful).
func TestStatusFileMode0660(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: defaultListen,
		accepting:  true,
		modules:    map[string]usbip.ModuleState{testUSBIPCoreModule: usbip.ModuleStateLoaded},
	}

	cleanup := startStatusTestServer(t, src, sockPath)
	t.Cleanup(cleanup)

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	// Mode should be 0660 on the permission bits (ignore type bits).
	require.Equal(t, os.FileMode(0o660), info.Mode().Perm())
}

// TestStatusGroupChownSkipsIfMissing proves the status server handles
// a missing --status-socket-group gracefully (best-effort chown, not
// a hard failure — operators on dev machines without a `usbip` group
// should still see a usable endpoint).
func TestStatusGroupChownSkipsIfMissing(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: defaultListen,
		accepting:  true,
		modules:    map[string]usbip.ModuleState{testUSBIPCoreModule: usbip.ModuleStateLoaded},
	}

	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = serveStatus(ctx, sockPath, "definitely-not-a-real-group-xyz", src, started)
	}()

	// Cleanup order matters: LIFO, so the first-registered hook fires
	// last. We need cancel() to run BEFORE <-done (otherwise the goroutine
	// never exits and <-done deadlocks). Registering cancel last makes it
	// fire first.
	t.Cleanup(func() { <-done })
	t.Cleanup(cancel)

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("status server did not start")
	}

	// Sanity ping.
	client := newUDSHTTPClient(sockPath)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://usbipd/", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestStatusBindSerialisedByFlock proves the TOCTOU-free bind path:
// two concurrent serveStatus goroutines pointed at the same path MUST
// serialise — exactly one binds and serves, the other observes
// errAlreadyRunning (or equivalent) without racing the first daemon's
// detect/unlink/bind sequence.
func TestStatusBindSerialisedByFlock(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: defaultListen,
		accepting:  true,
		modules:    map[string]usbip.ModuleState{testUSBIPCoreModule: usbip.ModuleStateLoaded},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const racers = 2

	results := make(chan error, racers)
	started := make(chan struct{})

	var wg sync.WaitGroup

	for range racers {
		wg.Go(func() {
			results <- serveStatus(ctx, sockPath, "", src, started)
		})
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("neither status server bound within 2s")
	}

	var loserErr error

	select {
	case loserErr = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("competing status server did not complete collision detection")
	}

	require.ErrorIs(t, loserErr, errAlreadyRunning,
		"the losing daemon must observe the live winner")

	cancel()
	wg.Wait()
	close(results)

	winnerErr := <-results
	require.True(t, winnerErr == nil || isClosedErr(winnerErr),
		"winning status server must shut down cleanly, got %v", winnerErr)
}

// TestStatusGroupChownResolvesCallerGroup proves applyStatusSocketACL
// silently completes when handed a group the current process belongs to.
// A same-uid chgrp to one of the caller's own groups does NOT need root,
// so this test is deterministic on every Linux host (CI + dev) without
// the /etc/group-dependent skip dance the earlier design implied.
func TestStatusGroupChownResolvesCallerGroup(t *testing.T) {
	t.Parallel()

	gids, err := os.Getgroups()
	require.NoError(t, err)
	require.NotEmpty(t, gids, "process must belong to at least one group")

	// LookupGroupId to resolve a *name* we can hand to applyStatusSocketACL.
	// Walk every supplementary group until LookupGroupId succeeds — some
	// NSS backends omit select gids from the name map.
	var gname string

	for _, gid := range gids {
		grp, gerr := user.LookupGroupId(strconv.Itoa(gid))
		if gerr == nil {
			gname = grp.Name

			break
		}
	}

	require.NotEmpty(t, gname, "unable to resolve any caller group to a name")

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	// Create the file mode 0600 so the chown-only helper has a valid
	// target. We're unit-testing the group-chown helper in isolation;
	// mode is now set by bindStatusSocket's post-bind os.Chmod so the
	// ACL helper no longer touches permissions. filepath.Clean
	// reassures gosec G304 that the test-generated path has no
	// traversal. 0o600 avoids gosec G302 "file permissions 0600 or
	// less" — the mode under test here is chown, not chmod.
	f, err := os.OpenFile(filepath.Clean(sockPath),
		os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	applyStatusSocketACL(t.Context(), sockPath, gname)

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	// Mode is untouched by the chown-only helper; 0o600 remains.
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestApplyStatusSocketACL_UnknownGroupRoutesViaCtxLogger covers the
// group-lookup-failed branch and the loggerOrDefault ctx-bound path:
// when LookupGroup errors and the ctx carries a logger, the helper
// MUST emit via the configured logger (verified by capturing its
// output) rather than slog.Default. Ensures the unified-log fix
// for `applyStatusSocketACL` actually reaches the operator's
// configured handler.
func TestApplyStatusSocketACL_UnknownGroupRoutesViaCtxLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	captured := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx := context.WithValue(t.Context(), loggerContextKey{}, captured)

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	f, err := os.OpenFile(filepath.Clean(sockPath), os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	// Group name guaranteed not to exist on any host; LookupGroup MUST
	// fail and the helper MUST log via the ctx-bound logger.
	applyStatusSocketACL(ctx, sockPath, "definitely-not-a-real-group-7f9a3b21")

	require.Contains(t, buf.String(), "status-socket group lookup failed",
		"unknown group lookup must emit via the ctx-bound logger, not slog.Default")
	require.Contains(t, buf.String(), "definitely-not-a-real-group-7f9a3b21",
		"the failed group name must appear in the structured log payload")
}

// TestApplyStatusSocketACL_EmptyGroupShortCircuits pins the
// no-op fast path: when no group is configured the helper MUST
// return immediately without attempting any lookup, any chown, or
// any logging. A captured ctx-bound logger lets us prove silence —
// asserting non-emission rather than just non-panic.
func TestApplyStatusSocketACL_EmptyGroupShortCircuits(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	captured := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := context.WithValue(t.Context(), loggerContextKey{}, captured)

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	f, err := os.OpenFile(filepath.Clean(sockPath), os.O_CREATE|os.O_RDWR, 0o600)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	applyStatusSocketACL(ctx, sockPath, "")

	require.Empty(t, buf.String(),
		"empty group MUST short-circuit before any logging — the helper "+
			"is a no-op when the operator hasn't configured a group ACL")

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestStatusDrainRejectsQueryParams pins the v1 freeze: POST /drain
// MUST NOT silently accept query parameters. A future operator who
// types `usbip drain --force` against a build that does not
// understand `?force=true` should get a clear 400 Bad Request, not
// a silent success that makes them think force took effect.
// OpenSpec keeps the door open for v2 to add typed flags without
// the silent-accept ambiguity.
func TestStatusDrainRejectsQueryParams(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: defaultListen,
		accepting:  true,
		modules:    map[string]usbip.ModuleState{testUSBIPCoreModule: usbip.ModuleStateLoaded},
	}

	cleanup := startStatusTestServer(t, src, sockPath)
	t.Cleanup(cleanup)

	client := newUDSHTTPClient(sockPath)

	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, "http://usbipd/drain?force=true", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"POST /drain with query params must surface 400 so silent-accept ambiguity cannot mask an unrecognised future flag")

	require.Equalf(t, int32(0), src.drainCalled.Load(),
		"rejected request must NOT trigger Drain")
}

// TestStatusDrainHandlerIdempotent locks OpenSpec's idempotency
// guarantee: repeated POST /drain calls return 200 each time but the
// underlying Drain operation runs at most once. Without the
// handler-level guard, every POST would spawn a fresh goroutine that
// calls Drain — wasted goroutines and duplicate error logs on the
// rare path where Drain returns non-nil.
func TestStatusDrainHandlerIdempotent(t *testing.T) {
	t.Parallel()

	dir := shortSocketTempDir(t)
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: defaultListen,
		accepting:  true,
		modules:    map[string]usbip.ModuleState{testUSBIPCoreModule: usbip.ModuleStateLoaded},
	}

	cleanup := startStatusTestServer(t, src, sockPath)
	t.Cleanup(cleanup)

	client := newUDSHTTPClient(sockPath)

	const drainCalls = 10

	// Fan out concurrent POSTs and collect (status, error) tuples on a
	// channel. The require-* assertions run in the test goroutine
	// after the fan-in so testifylint's go-require rule is satisfied.
	type drainResult struct {
		status int
		err    error
	}

	results := make(chan drainResult, drainCalls)

	var wg sync.WaitGroup

	for range drainCalls {
		wg.Go(func() {
			req, reqErr := http.NewRequestWithContext(context.Background(),
				http.MethodPost, "http://usbipd/drain", nil)
			if reqErr != nil {
				results <- drainResult{err: reqErr}

				return
			}

			resp, doErr := client.Do(req)
			if doErr != nil {
				results <- drainResult{err: doErr}

				return
			}

			defer func() { _ = resp.Body.Close() }()

			results <- drainResult{status: resp.StatusCode}
		})
	}

	wg.Wait()
	close(results)

	// Two distinct status codes by RFC 9110 §15.3 semantics: the
	// FIRST POST that flips the gate gets 202 Accepted (drain
	// queued, processing async); subsequent POSTs that find the
	// gate already set get 200 OK (no-op idempotent acknowledgement).
	// Operators distinguish "I started this drain" from "someone
	// else already started it" without parsing a response body.
	var (
		accepted int
		ok       int
	)

	for r := range results {
		require.NoError(t, r.err)

		switch r.status {
		case http.StatusAccepted:
			accepted++
		case http.StatusOK:
			ok++
		default:
			t.Fatalf("unexpected status %d (want 200 or 202)", r.status)
		}
	}

	require.Equalf(t, 1, accepted,
		"exactly one POST must receive 202 (the one that won the gate)")
	require.Equalf(t, drainCalls-1, ok,
		"every other POST must receive 200 idempotent ack")

	// Exactly one 202 response proves exactly one request won the CAS and
	// launched Drain. Wait only for that accepted request's asynchronous
	// call to become observable; no timing window is needed because every
	// other handler already returned from the no-launch branch.
	require.Eventually(t, func() bool {
		return src.drainCalled.Load() == 1
	}, time.Second, 10*time.Millisecond,
		"the accepted drain request must invoke Drain exactly once")

	require.Equalf(t, int32(1), src.drainCalled.Load(),
		"POST /drain must be idempotent at the handler level — got %d Drain invocations across %d POSTs",
		src.drainCalled.Load(), drainCalls)
}
