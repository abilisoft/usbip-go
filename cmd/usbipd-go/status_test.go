// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
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
	mu          sync.Mutex
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

	return out, nil
}

func (f *fakeStatusSource) Drain(_ context.Context) error {
	f.drainCalled.Add(1)

	return f.drainErr
}

// udsHTTPClientTimeout caps every helper HTTP call so a broken server
// can't wedge the test suite.
const udsHTTPClientTimeout = 5 * time.Second

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

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	// Leave a stale file at sockPath with no listener on it. Path is
	// rooted at t.TempDir, so gosec G304's "variable inclusion" is a
	// false positive here — the filename is test-controlled, not
	// attacker-derived.
	f, err := os.Create(filepath.Clean(sockPath))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules: map[string]usbip.ModuleState{
			"usbip_core": usbip.ModuleStateLoaded,
			"vhci_hcd":   usbip.ModuleStateLoaded,
			"usbip_host": usbip.ModuleStateLoaded,
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

	dir := t.TempDir()
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

// testSessionID is a fixed 16-byte UUID used across JSON golden tests
// so the expected renders stay deterministic.
var testSessionID = domain.SessionID{
	0x01, 0x02, 0x03, 0x04,
	0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c,
	0x0d, 0x0e, 0x0f, 0x10,
}

// TestStatusGetJSON proves the §7.7 schema-v1 JSON is rendered by
// GET /. Every required key is asserted.
func TestStatusGetJSON(t *testing.T) {
	t.Parallel()

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
				ID:         testSessionID,
				BusID:      usbip.BusID("1-1.2"),
				RemoteAddr: netip.MustParseAddrPort("10.0.0.8:54221"),
				StartedAt:  time.Unix(1_700_000_000, 0),
			},
		},
		modules: map[string]usbip.ModuleState{
			"usbip_core": usbip.ModuleStateLoaded,
			"vhci_hcd":   usbip.ModuleStateLoaded,
			"usbip_host": usbip.ModuleStateMissing,
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
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules:    map[string]usbip.ModuleState{"usbip_core": usbip.ModuleStateLoaded},
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
// Group chown is best-effort; we check that if the "usbip-go" group
// exists, the bind succeeds (a stricter chown-applied assertion would
// require root to be meaningful).
func TestStatusFileMode0660(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules:    map[string]usbip.ModuleState{"usbip_core": usbip.ModuleStateLoaded},
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
// a hard failure — operators on dev machines without a `usbip-go` group
// should still see a usable endpoint).
func TestStatusGroupChownSkipsIfMissing(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules:    map[string]usbip.ModuleState{"usbip_core": usbip.ModuleStateLoaded},
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

	dir := t.TempDir()
	sockPath := filepath.Join(dir, "status.sock")

	src := &fakeStatusSource{
		listenAddr: "0.0.0.0:3240",
		accepting:  true,
		modules:    map[string]usbip.ModuleState{"usbip_core": usbip.ModuleStateLoaded},
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	type result struct {
		err     error
		started bool
	}

	const racers = 2

	results := make(chan result, racers)

	var wg sync.WaitGroup

	for range racers {
		wg.Go(func() {
			started := make(chan struct{})

			doneStart := make(chan struct{})

			go func() {
				select {
				case <-started:
				case <-time.After(2 * time.Second):
				}

				close(doneStart)
			}()

			err := serveStatus(ctx, sockPath, "", src, started)

			<-doneStart

			results <- result{err: err, started: true}
		})
	}

	// Give both goroutines a chance to reach the bind path, then
	// cancel so the winning serveStatus returns cleanly.
	time.Sleep(300 * time.Millisecond)
	cancel()
	wg.Wait()
	close(results)

	var (
		loserErrs int
		winnerOK  int
	)

	for r := range results {
		switch {
		case r.err == nil:
			winnerOK++
		case errors.Is(r.err, errAlreadyRunning):
			loserErrs++
		default:
			// A context.Canceled or closed-listener return from the
			// winner is indistinguishable from nil for this test; any
			// other error from the loser would indicate a TOCTOU race
			// surfacing through bind / unlink.
			if isClosedErr(r.err) {
				winnerOK++
			} else {
				t.Errorf("unexpected serveStatus error: %v", r.err)
			}
		}
	}

	require.Equal(t, 1, winnerOK, "expected exactly one daemon to bind")
	require.Equal(t, 1, loserErrs,
		"expected the other daemon to see errAlreadyRunning, got %d", loserErrs)
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

	dir := t.TempDir()
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

	applyStatusSocketACL(sockPath, gname)

	info, err := os.Stat(sockPath)
	require.NoError(t, err)
	// Mode is untouched by the chown-only helper; 0o600 remains.
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
