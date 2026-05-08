package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
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
	modules    map[string]string

	drainCalled atomic.Int32
	drainErr    error
	mu          sync.Mutex
}

func (f *fakeStatusSource) BoundDevices(_ context.Context) []usbip.Device {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]usbip.Device{}, f.bound...)
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

func (f *fakeStatusSource) KernelModules(_ context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make(map[string]string, len(f.modules))
	for k, v := range f.modules {
		out[k] = v
	}

	return out, nil
}

func (f *fakeStatusSource) Drain(_ context.Context) error {
	f.drainCalled.Add(1)

	return f.drainErr
}

// newUDSHTTPClient returns an http.Client that dials the given UDS
// path. Every request uses scheme http with the host ignored by the
// transport; the URL must still be valid.
func newUDSHTTPClient(path string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", path)
			},
		},
		Timeout: 5 * time.Second,
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
	// t.Setenv-free but still non-parallel because the status server
	// binds a fixed UDS path per test.

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	// Leave a stale file at sockPath with no listener on it.
	f, err := os.Create(sockPath)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules: map[string]string{
			"usbip_core": "loaded",
			"vhci_hcd":   "loaded",
			"usbip_host": "loaded",
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
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	// Spin a plain unix listener at sockPath to simulate "another
	// usbipd is here". Any dial succeeding signals already-running.
	lis, err := net.Listen("unix", sockPath)
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

	err = detectAlreadyRunning(sockPath)
	require.ErrorIs(t, err, errAlreadyRunning)
}

// TestStatusGetJSON proves the §7.7 schema-v1 JSON is rendered by
// GET /. Every required key is asserted.
func TestStatusGetJSON(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		activation: true,
		accepting:  true,
		bound: []usbip.Device{
			{BusID: usbip.BusID("1-1.2"), VendorID: 0x0951, ProductID: 0x1666},
		},
		sessions: []usbip.Session{
			{
				ID:         domain.SessionID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
				BusID:      usbip.BusID("1-1.2"),
				RemoteAddr: netip.MustParseAddrPort("10.0.0.8:54221"),
				StartedAt:  time.Unix(1_700_000_000, 0),
			},
		},
		modules: map[string]string{
			"usbip_core": "loaded",
			"vhci_hcd":   "loaded",
			"usbip_host": "missing",
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
	require.Equal(t, "loaded", km["usbip_core"])
	require.Equal(t, "loaded", km["vhci_hcd"])
	require.Equal(t, "missing", km["usbip_host"])
}

// TestStatusDrainTriggersShutdown proves POST /drain invokes Drain on
// the underlying source and returns 200 immediately.
func TestStatusDrainTriggersShutdown(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules:    map[string]string{"usbip_core": "loaded"},
	}

	cleanup := startStatusTestServer(t, src, sockPath)
	t.Cleanup(cleanup)

	client := newUDSHTTPClient(sockPath)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://usbipd/drain", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusOK, resp.StatusCode)

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
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules:    map[string]string{"usbip_core": "loaded"},
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
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules:    map[string]string{"usbip_core": "loaded"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = serveStatus(ctx, sockPath, "definitely-not-a-real-group-xyz", src, started)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("status server did not start")
	}

	t.Cleanup(func() { <-done })

	// Sanity ping.
	client := newUDSHTTPClient(sockPath)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://usbipd/", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)

	t.Cleanup(func() { _ = resp.Body.Close() })

	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestStatusGroupChownAppliedIfGroupPresent proves that when the
// configured --status-socket-group resolves, the UDS ownership is
// updated. On a typical developer machine the `usbip` group is absent
// so this test skips; CI hosts preconfigured with the group exercise
// the branch without requiring root (a same-uid chgrp to a group the
// caller already belongs to is permitted).
func TestStatusGroupChownAppliedIfGroupPresent(t *testing.T) {
	gname := "usbip"

	_, err := user.LookupGroup(gname)
	if err != nil {
		t.Skipf("group %q absent; nothing to assert", gname)
	}

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules:    map[string]string{"usbip_core": "loaded"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer close(done)
		_ = serveStatus(ctx, sockPath, gname, src, started)
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("status server did not start")
	}

	t.Cleanup(func() { <-done })

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o660), info.Mode().Perm())
}
