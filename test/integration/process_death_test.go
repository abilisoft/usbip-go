// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/test/integration"
	"github.com/stretchr/testify/require"
)

// killHelperBinary is the name of the subprocess cmd/usbip-test-killable
// produces. The test builds it to t.TempDir via `go build` so there is
// no dependency on the repo root binary being fresh.
const killHelperBinary = "usbip-test-killable"

// killHelperEnvKillAt mirrors cmd/usbip-test-killable's killEnv.
const killHelperEnvKillAt = "USBIP_TEST_KILL_AT"

// killHelperEnvServer mirrors cmd/usbip-test-killable's serverEnv.
const killHelperEnvServer = "USBIP_TEST_SERVER"

// killHelperEnvBusID mirrors cmd/usbip-test-killable's busIDEnv.
const killHelperEnvBusID = "USBIP_TEST_BUSID"

// killWaitDeadline bounds the per-checkpoint stderr wait and the
// post-SIGKILL reap. The helper binary prints AT=<point> immediately
// on reaching each checkpoint, so three seconds is plenty; longer
// budgets would hide a real hang.
const killWaitDeadline = 3 * time.Second

// TestProcessDeathBeforeDial covers spec §5.4 item 5a's before-dial
// branch: the child dies BEFORE the Importer ever opens a TCP
// connection to the server, so the kernel neither hands out a vhci
// port nor opens a stub socket. Assertion is negative — no port
// appears in /sys/devices/platform/vhci_hcd.0/status for the child's
// timeline.
func TestProcessDeathBeforeDial(t *testing.T) {
	integration.SetupVUDC(t)

	server, serverAddr := startFakeOpRepImportServer(t)
	defer server.close()

	helper := buildKillHelper(t)

	runKillScenario(t, helper, killScenario{
		killAt: "before_dial",
		server: serverAddr,
		busID:  "usbip-vudc.0",
		// Before dial: the child must NOT have produced any TCP
		// connection to the server. The fake server observes zero
		// accepts for the child's timeline.
		assertPost: func(t *testing.T) {
			t.Helper()

			require.Zero(t, server.accepts(), "before_dial: server must not see any accepted connection")
		},
	})
}

// TestProcessDeathAfterDial covers the mid-handshake branch: the
// child successfully dials AND the server accepts the connection,
// but SIGKILL fires before AttachRemote's sysfs handoff. The kernel
// closes the fd (refcount 0), which the server observes as RST/FIN
// — assertion: the server's accepted connection closes within the
// kill deadline.
func TestProcessDeathAfterDial(t *testing.T) {
	integration.SetupVUDC(t)

	server, serverAddr := startFakeOpRepImportServer(t)
	defer server.close()

	helper := buildKillHelper(t)

	runKillScenario(t, helper, killScenario{
		killAt: "after_dial",
		server: serverAddr,
		busID:  "usbip-vudc.0",
		assertPost: func(t *testing.T) {
			t.Helper()

			// Server accepted at least one connection (the dial
			// succeeded) and that connection is now closed because
			// the child died.
			require.Eventually(t, func() bool {
				return server.accepts() >= 1 && server.closed()
			}, killWaitDeadline, 50*time.Millisecond,
				"after_dial: server must observe accepted-then-closed connection")
		},
	})
}

// TestProcessDeathCheckpointAnnouncedBeforeFailingOp pins the invariant
// that the helper announces AT=<checkpoint> BEFORE the op whose failure
// would otherwise skip the announce. The scenario points the child at a
// listener that is bound then immediately closed so any dial attempt
// from the Importer fails with ECONNREFUSED; with USBIP_TEST_KILL_AT
// set to after_sysfs the pre-fix helper exits via exitAttachFailed
// without ever writing AT=after_sysfs, which hangs the parent on its
// stderr read. Asserting the parent observes the AT line within a tight
// 1-second budget guards the announce-before-op ordering required by
// spec §5.4 item 5a / item 7 telemetry.
func TestProcessDeathCheckpointAnnouncedBeforeFailingOp(t *testing.T) {
	// No SetupVUDC / kernel module preflight: this test asserts on the
	// helper's stderr ordering only, and Attach aborts at dial (before
	// any sysfs write) because the fake server is unreachable. Keeping
	// the test kernel-free keeps it runnable on non-root CI where the
	// rest of the integration suite skips on configfs permission.

	// Bind 127.0.0.1:0 so we get a free-port reservation, then close
	// so any later dial returns ECONNREFUSED deterministically.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	refusedAddr := lis.Addr().String()

	require.NoError(t, lis.Close())

	helper := buildKillHelper(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, helper)
	cmd.Env = append(os.Environ(),
		killHelperEnvKillAt+"=after_sysfs",
		killHelperEnvServer+"="+refusedAddr,
		killHelperEnvBusID+"=usbip-vudc.0",
	)

	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)

	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}

		_ = cmd.Wait()
	})

	// Tight 1s budget — ECONNREFUSED fires synchronously on loopback
	// within tens of milliseconds; the announce must beat the parent's
	// stderr read regardless of Attach's outcome.
	announceCh := make(chan string, 1)

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "AT=after_sysfs") {
				announceCh <- line

				return
			}
		}

		close(announceCh)
	}()

	select {
	case line, ok := <-announceCh:
		require.True(t, ok, "helper stderr closed before announcing AT=after_sysfs")
		require.Equal(t, "AT=after_sysfs", line)
	case <-time.After(1 * time.Second):
		t.Fatal("helper did not announce AT=after_sysfs within 1s despite Attach failure; parent would hang indefinitely")
	}
}

// killScenario packages the per-test knobs runKillScenario needs. One
// struct keeps the argument list short and lets new checkpoints slot
// in by adding a case to the assertPost handler.
type killScenario struct {
	killAt     string
	server     string
	busID      string
	assertPost func(t *testing.T)
}

// runKillScenario starts the helper binary, synchronises on the
// checkpoint line, SIGKILLs, waits for reap, and runs assertPost.
// Parameterised by the scenario so each t.Run case adds only the
// checkpoint name and the assertion closure.
func runKillScenario(t *testing.T, helper string, sc killScenario) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, helper)
	cmd.Env = append(os.Environ(),
		killHelperEnvKillAt+"="+sc.killAt,
		killHelperEnvServer+"="+sc.server,
		killHelperEnvBusID+"="+sc.busID,
	)

	stderr, err := cmd.StderrPipe()
	require.NoError(t, err)

	err = cmd.Start()
	require.NoError(t, err)

	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}

		_ = cmd.Wait()
	})

	// Block on the AT=<killAt> line so we know the child is parked at
	// the checkpoint before we SIGKILL. Any earlier kill would catch
	// the child at a different state, which would make the
	// assertPost non-deterministic.
	waitForCheckpoint(t, stderr, sc.killAt)

	// SIGKILL guarantees no graceful shutdown path fires between our
	// signal and process termination. The child's fds (TCP socket,
	// any future sysfs write) are closed by the kernel at process
	// exit; that is the behaviour the spec pins down.
	require.NoError(t, cmd.Process.Signal(syscall.SIGKILL))

	err = cmd.Wait()
	// Wait returns an error because SIGKILL gives a non-zero exit;
	// that is expected. The test asserts on the server-side effect
	// after the child is reaped.
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("unexpected wait error: %v", err)
	}

	sc.assertPost(t)
}

// waitForCheckpoint reads stderr until an AT=<point> line is seen.
// Times out with a clear message after killWaitDeadline so a helper
// that fails to reach the checkpoint (e.g. build drift) does not hang
// the test.
func waitForCheckpoint(t *testing.T, r io.Reader, want string) {
	t.Helper()

	want = "AT=" + want

	done := make(chan struct{})

	go func() {
		defer close(done)

		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			line := scanner.Text()

			// Helper binary prints multiple AT= lines as it
			// progresses; stop at the requested one.
			if strings.HasPrefix(line, "AT=") && line == want {
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(killWaitDeadline):
		t.Fatalf("helper did not reach checkpoint %q within %s", want, killWaitDeadline)
	}
}

// buildKillHelper compiles cmd/usbip-test-killable into t.TempDir so
// each test run gets a fresh binary that matches the current source.
// Avoids leaning on the repo-root bin/ layout.
func buildKillHelper(t *testing.T) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), killHelperBinary)

	// go test typically runs from the package directory; traverse
	// upward to find the module root so `go build` resolves the
	// cmd path. Three hops covers test/integration/..
	repoRoot := findModuleRoot(t)

	// -buildvcs=false: the helper runs in environments (integration
	// microVM, CI containers, bind-mounted worktrees) where `go build`
	// cannot reach a usable .git tree under the test UID, and the
	// stamp would abort the build with "error obtaining VCS status".
	// The helper does not surface version information — no value lost.
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, "./cmd/usbip-test-killable/")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "build helper: %s", output)

	return out
}

// findModuleRoot walks up from the test's cwd until it finds a
// go.mod file, then returns that directory. Caches nothing because
// the lookup cost is negligible next to the `go build` that follows.
func findModuleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for i := 0; i < 10; i++ {
		_, statErr := os.Stat(filepath.Join(dir, "go.mod"))
		if statErr == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}

		dir = parent
	}

	t.Fatalf("module root (go.mod) not found above cwd")

	return ""
}

// fakeOpRepImportServer is a loopback TCP server that accepts any
// client, parses the OP_REQ_IMPORT header, and writes a minimal
// OP_REP_IMPORT reply so the child's Importer progresses to the
// AttachRemote kernel call. Tracks accepts and closes so the tests
// can assert on observable side-effects.
//
// nAccepts and nClosed are protected by mu — acceptLoop increments
// them from a dedicated goroutine, while the test body reads them
// from `require.Eventually`'s polling goroutine. -race would flag
// the unsynchronised access otherwise.
type fakeOpRepImportServer struct {
	lis      net.Listener
	mu       sync.Mutex
	nAccepts int
	nClosed  int
	done     chan struct{}
}

// startFakeOpRepImportServer binds 127.0.0.1:0 and spawns a goroutine
// that handles one connection at a time. Returns the server handle
// and the "host:port" string the child should dial.
func startFakeOpRepImportServer(t *testing.T) (*fakeOpRepImportServer, string) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &fakeOpRepImportServer{lis: lis, done: make(chan struct{})}

	go srv.acceptLoop()

	// Returning the listener's string address so the child can dial
	// the kernel-picked port without the test itself needing to
	// assemble it.
	addr := lis.Addr().String()

	t.Cleanup(func() { srv.close() })

	return srv, addr
}

// acceptLoop accepts successive connections, handles them, and
// increments counters under the server's mutex. Exits when Close
// fires and the listener returns net.ErrClosed.
func (s *fakeOpRepImportServer) acceptLoop() {
	defer close(s.done)

	for {
		conn, err := s.lis.Accept()
		if err != nil {
			return
		}

		s.mu.Lock()
		s.nAccepts++
		s.mu.Unlock()

		s.handle(conn)

		s.mu.Lock()
		s.nClosed++
		s.mu.Unlock()
	}
}

// handle services one child: read the 40-byte OP_REQ_IMPORT, send
// back a 320-byte OP_REP_IMPORT with a canned device. If the child
// dies mid-read, the read returns EOF and we fall through to
// Close — that is the "after_dial" branch's observable effect.
func (s *fakeOpRepImportServer) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()

	buf := make([]byte, 40)

	_, readErr := io.ReadFull(conn, buf)
	if readErr != nil {
		// child died before sending its OP_REQ_IMPORT; that's a
		// valid scenario. handle exits and nClosed increments.
		return
	}

	// Encode a bare OP_REP_IMPORT with a zeroed device. The child's
	// Importer will proceed to AttachRemote which fails against the
	// fake server (no kernel handoff possible), but the test doesn't
	// care — we SIGKILL before the assertion pipeline tests the
	// kernel side-effect.
	var dev domain.Device
	dev.BusID = domain.BusID("usbip-vudc.0")
	dev.Speed = domain.SpeedHigh

	codec := &wire.Codec{}

	_ = codec.EncodeOpRepImport(conn, dev)
}

// accepts returns the current accept counter. Safe to call after
// close because the acceptLoop owns the increment and exits at Close.
func (s *fakeOpRepImportServer) accepts() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.nAccepts
}

// closed reports whether at least one accepted connection has been
// closed. Used by after_dial to assert "server observed RST/FIN".
func (s *fakeOpRepImportServer) closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.nClosed >= 1
}

// close tears the listener down and waits for the acceptLoop to exit.
// Safe to call multiple times via the lis.Close idempotency + done
// channel check.
func (s *fakeOpRepImportServer) close() {
	_ = s.lis.Close()

	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
	}
}

// runtimeGOOSLinuxGuard is a belt-and-braces compile-time check: the
// killable test only makes sense on Linux because fd-lifecycle
// semantics differ on other kernels. The build tag already gates
// this file; the constant assertion prevents a future removal of the
// tag from silently letting the test compile on Darwin.
const runtimeGOOSLinuxGuard = "linux"

var _ = func() { _ = runtimeGOOSLinuxGuard == runtime.GOOS }

// fmt pulled in for error formatting shared across scenarios.
var _ = fmt.Sprint
