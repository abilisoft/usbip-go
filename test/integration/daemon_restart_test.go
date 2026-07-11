// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/abilisoft/usbip-go/test/integration"
	"github.com/stretchr/testify/require"
)

const (
	// daemonRestartDeadline bounds the full scenario. Three daemon starts
	// plus one attach and one recovery sequence is comfortably under this
	// budget even on slow VMs.
	daemonRestartDeadline = 30 * time.Second

	// daemonStateDeadline bounds individual userspace-to-kernel state
	// transitions within the larger restart scenario.
	daemonStateDeadline = 5 * time.Second
	daemonPollInterval  = 50 * time.Millisecond

	// usbipExporterStatusUsed is SDEV_ST_USED from the Linux usbip-host
	// sysfs ABI. Reaching this state proves the daemon completed its
	// socket handoff before the test terminates the process.
	usbipExporterStatusAvailable = "1"
	usbipExporterStatusUsed      = "2"
	usbipSockfdDisconnect        = "-1"
	usbipSysfsWriteMode          = 0o200
)

// daemonStartSignal is the substring the daemon logs when its
// accept-path listener is bound — used as a synchronisation point so
// the importer's Attach dials only after the daemon is ready. Matches
// the log line emitted by cmd/usbip-go/serve.go's "usbip-go serve
// accepting connections" info log.
const daemonStartSignal = "usbip-go serve accepting connections"

// TestDaemonRestartRequiresSessionReconnect pins the observed Linux
// lifecycle across an abrupt exporter process restart. Kernel socket
// handoff keeps traffic outside the userspace data path while the daemon
// is running; it does not make the active remote attachment portable to
// a replacement process. The client port is released after SIGKILL, while
// the exporter-side kernel session requires explicit reconciliation before
// the client can attach to the replacement daemon.
//
// Test flow:
//  1. Harness vudc + env-supplied usbip-host busid.
//  2. Start our usbip-go serve subprocess on 127.0.0.1:0 with the busid
//     already bound (Bind happens from the parent before spawning).
//  3. Parent (as importer) attaches the device via TCP.
//  4. Kill the daemon (SIGKILL).
//  5. Assert the client VHCI port is released, then reconcile the orphaned
//     exporter kernel session through usbip_sockfd=-1.
//  6. Start a replacement daemon subprocess on the same address.
//  7. Attach again and prove the replacement session reaches both
//     client and exporter kernel-owned states.
//  8. Cleanup.
//
// Env-gated on USBIPGO_INTEGRATION_BUSID because the scenario
// requires usbip-host semantics; vudc-only devices do not carry a
// usbip_sockfd attribute.
func TestDaemonRestartRequiresSessionReconnect(t *testing.T) {
	integration.SetupVUDC(t)

	busID := integration.RequireRealBusID(t)

	ctx, cancel := context.WithTimeout(context.Background(), daemonRestartDeadline)
	defer cancel()

	// Build the daemon once per test-run into t.TempDir so there is
	// no dependency on bin/usbip-go being up-to-date.
	daemonBinary := buildDaemonBinary(t)

	// Pre-bind the device via a short-lived Exporter. The daemon
	// subprocesses don't bind at startup — they only Serve — so a
	// parent-side bind is the cleanest way to put the kernel in the
	// "usbip-host has matched busid" state.
	bindExporter, err := usbip.NewExporter()
	require.NoError(t, err)

	integration.RequireBindable(t, ctx, bindExporter, busID)

	// Start daemon #1 on a parent-chosen port so daemon #2 can
	// reuse the same addr after SIGKILL.
	daemon1, addr, err := startDaemonSubprocess(t, daemonBinary, "127.0.0.1:0")
	require.NoError(t, err)

	// Importer attaches via real TCP.
	imp, err := usbip.NewImporter()
	require.NoError(t, err)

	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(ctx, addr, busID, usbip.AttachOptions{})
	require.NoError(t, err)
	require.Equal(t, busID, port.BusID)
	require.Equal(t, domain.StatusUsed, port.Status)

	// The importer can finish its own handshake and VHCI handoff after
	// reading OP_REP_IMPORT but before the exporter subprocess completes
	// its usbip_sockfd write. Killing the subprocess in that window tests
	// an interrupted handoff, not daemon-restart survival. Wait for the
	// exporter-side kernel state to prove both kernels own socket refs.
	waitForExporterKernelStatus(t, busID, usbipExporterStatusUsed)

	// SIGKILL daemon #1. Linux tears down this exported connection and the
	// remote VHCI port transitions back to a free state.
	require.NoError(t, daemon1.proc.Signal(syscall.SIGKILL))

	_, err = daemon1.proc.Wait()
	// SIGKILL produces a non-zero exit; only unexpected wait errors
	// (e.g. ECHILD) bubble up as test failures.
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("daemon1 Wait: %v", err)
	}

	waitForClientPortRelease(t, ctx, imp, port.ID)
	reconcileExporterKernelSession(t, busID)
	waitForExporterKernelStatus(t, busID, usbipExporterStatusAvailable)

	// Start daemon #2 reusing the same port. bindReplacementExporter
	// was our in-test loopback reuse helper; the equivalent for the
	// external binary is a direct exec with the same addr.
	daemon2, _, err := startDaemonSubprocess(t, daemonBinary, fmt.Sprintf("127.0.0.1:%d", addr.Port))
	if err != nil {
		// Port reuse can briefly return EADDRINUSE while the kernel
		// tears down daemon1's listener; retry with backoff.
		require.Eventually(t, func() bool {
			d, _, dErr := startDaemonSubprocess(t, daemonBinary, fmt.Sprintf("127.0.0.1:%d", addr.Port))
			if dErr != nil {
				return false
			}

			daemon2 = d

			return true
		}, 5*time.Second, 200*time.Millisecond, "daemon2 bind retry: %v", err)
	}

	require.NotNil(t, daemon2, "daemon2 must start")

	replacement, err := imp.Attach(ctx, addr, busID, usbip.AttachOptions{})
	require.NoError(t, err)
	require.Equal(t, busID, replacement.BusID)
	require.Equal(t, domain.StatusUsed, replacement.Status)

	waitForExporterKernelStatus(t, busID, usbipExporterStatusUsed)
	require.NoError(t, imp.Detach(ctx, replacement.ID))
}

func waitForExporterKernelStatus(t *testing.T, busID domain.BusID, want string) {
	t.Helper()

	statusPath := exporterSysfsPath(busID, "usbip_status")
	deadline := time.NewTimer(daemonStateDeadline)
	ticker := time.NewTicker(daemonPollInterval)
	defer deadline.Stop()
	defer ticker.Stop()

	var lastErr error
	var lastStatus string

	for {
		lastStatus, lastErr = readExporterKernelStatus(statusPath)
		if lastErr == nil && lastStatus == want {
			return
		}

		select {
		case <-deadline.C:
			t.Fatalf(
				"exporter kernel status did not reach %q at %s; last status=%q last error=%v",
				want,
				statusPath,
				lastStatus,
				lastErr,
			)
		case <-ticker.C:
		}
	}
}

func reconcileExporterKernelSession(t *testing.T, busID domain.BusID) {
	t.Helper()

	statusPath := exporterSysfsPath(busID, "usbip_status")
	status, err := readExporterKernelStatus(statusPath)
	require.NoError(t, err)

	if status == usbipExporterStatusAvailable {
		return
	}

	require.Equal(t, usbipExporterStatusUsed, status,
		"abrupt daemon death must leave either an available or explicitly reconcilable export")

	sockfdPath := exporterSysfsPath(busID, "usbip_sockfd")
	require.NoError(t, os.WriteFile(
		sockfdPath,
		[]byte(usbipSockfdDisconnect),
		usbipSysfsWriteMode,
	), "reconcile orphaned exporter kernel session")
}

func exporterSysfsPath(busID domain.BusID, attribute string) string {
	return filepath.Join("/sys/bus/usb/devices", string(busID), attribute)
}

func readExporterKernelStatus(statusPath string) (string, error) {
	contents, err := os.ReadFile(statusPath)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(contents)), nil
}

func waitForClientPortRelease(
	t *testing.T,
	ctx context.Context,
	imp *usbip.Importer,
	portID domain.PortID,
) {
	t.Helper()

	require.Eventually(t, func() bool {
		ports, err := imp.ListPorts(ctx)
		if err != nil {
			return false
		}

		for _, port := range ports {
			if port.ID != portID {
				continue
			}

			return port.Status == domain.StatusNull ||
				port.Status == domain.StatusNotAssigned ||
				port.Status == domain.StatusAvailable
		}

		return true
	}, daemonStateDeadline, daemonPollInterval,
		"client VHCI port %d must be released after daemon death", portID)
}

// daemonSubprocess carries the exec.Cmd handle and the path to the
// daemon binary so t.Cleanup can reap the child on any exit path.
type daemonSubprocess struct {
	proc *os.Process
	cmd  *exec.Cmd
	done chan struct{}
	stop sync.Once
}

// startDaemonSubprocess spawns cmd/usbip-go at binary with --listen
// pointing at addr and waits for the daemon's "accepting connections"
// log before returning so callers can dial without racing the accept
// loop startup. addr of "127.0.0.1:0" asks the daemon to pick a port;
// the returned RemoteEndpoint carries the actual bound port.
func startDaemonSubprocess(
	t *testing.T, binary, addr string,
) (*daemonSubprocess, domain.RemoteEndpoint, error) {
	t.Helper()

	ctx := context.Background()

	cmd := exec.CommandContext(
		ctx,
		binary,
		"--log-format", "pretty",
		"--no-color",
		"serve",
		"--listen", addr,
		"--status-socket", "",
	)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, domain.RemoteEndpoint{}, fmt.Errorf("stderr pipe: %w", err)
	}

	startErr := cmd.Start()
	if startErr != nil {
		return nil, domain.RemoteEndpoint{}, fmt.Errorf("start daemon: %w", startErr)
	}

	s := &daemonSubprocess{proc: cmd.Process, cmd: cmd, done: make(chan struct{})}

	t.Cleanup(func() {
		s.stop.Do(func() {
			if s.proc != nil {
				_ = s.proc.Signal(syscall.SIGTERM)
			}

			_ = s.cmd.Wait()

			close(s.done)
		})
	})

	endpoint, waitErr := waitForDaemonReady(stderr, daemonStartSignal)
	if waitErr != nil {
		_ = s.proc.Signal(syscall.SIGTERM)

		return nil, domain.RemoteEndpoint{}, waitErr
	}

	return s, endpoint, nil
}

// waitForDaemonReady scans the daemon's stderr for the ready log line
// and parses the bound address out of it. The log format is
// structured slog text ("addr=127.0.0.1:33123"); we extract the
// addr=... token so the caller knows which port to dial.
func waitForDaemonReady(r pipeReader, signal string) (domain.RemoteEndpoint, error) {
	scanner := bufio.NewScanner(r)

	deadline := time.Now().Add(5 * time.Second)
	var observed []string

	for scanner.Scan() && time.Now().Before(deadline) {
		line := scanner.Text()
		observed = append(observed, line)

		if !strings.Contains(line, signal) {
			continue
		}

		endpoint, err := parseAddrFromReadyLog(line)
		if err != nil {
			return domain.RemoteEndpoint{}, err
		}

		return endpoint, nil
	}

	if err := scanner.Err(); err != nil {
		return domain.RemoteEndpoint{}, fmt.Errorf("scan daemon readiness log: %w", err)
	}

	return domain.RemoteEndpoint{}, fmt.Errorf(
		"%w; stderr=%q",
		errors.New("daemon ready signal not seen within 5s"),
		strings.Join(observed, "\n"),
	)
}

// parseAddrFromReadyLog extracts the addr=<host:port> token emitted
// by cmd/usbip-go/serve.go and converts it into a RemoteEndpoint. Lets
// callers dial the kernel-picked port without scraping the port out
// of the log themselves.
func parseAddrFromReadyLog(line string) (domain.RemoteEndpoint, error) {
	idx := strings.Index(line, "addr=")
	if idx < 0 {
		return domain.RemoteEndpoint{}, fmt.Errorf("no addr= in ready log: %s", line)
	}

	rest := line[idx+len("addr="):]

	// rest looks like "127.0.0.1:33123 activation=false" — trim at
	// the first space.
	if sp := strings.IndexByte(rest, ' '); sp > 0 {
		rest = rest[:sp]
	}

	host, portStr, splitErr := splitHostPortLog(rest)
	if splitErr != nil {
		return domain.RemoteEndpoint{}, splitErr
	}

	var port uint16

	_, scanErr := fmt.Sscanf(portStr, "%d", &port)
	if scanErr != nil {
		return domain.RemoteEndpoint{}, fmt.Errorf("parse port %q: %w", portStr, scanErr)
	}

	return domain.RemoteEndpoint{Host: host, Port: port}, nil
}

// splitHostPortLog is a bare-bones host:port splitter. Used for log
// parsing so we don't pull net.SplitHostPort's AddrError into the
// daemon-restart test's hot path.
func splitHostPortLog(s string) (string, string, error) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return s[:i], s[i+1:], nil
		}
	}

	return "", "", fmt.Errorf("no colon in %q", s)
}

// buildDaemonBinary compiles cmd/usbip-go into t.TempDir so daemon
// subprocesses run the exact source under test. Shares the
// findModuleRoot helper defined in process_death_test.go.
func buildDaemonBinary(t *testing.T) string {
	t.Helper()

	if bin, ok := integration.BazelRunfilePath(filepath.Join("cmd", "usbip-go", "usbip-go_", "usbip-go")); ok {
		return bin
	}

	out := filepath.Join(t.TempDir(), "usbip-go")
	repoRoot := findModuleRoot(t)

	// -buildvcs=false: see process_death_test.go buildKillHelper for
	// rationale — the helper runs where `go build`'s VCS stamp cannot
	// reach .git under the test UID, and version info is not surfaced
	// anywhere that would notice its absence.
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", out, "./cmd/usbip-go/")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "build usbip-go: %s", output)

	return out
}

// pipeReader is the minimal io.Reader surface waitForDaemonReady
// needs. Declared as an interface (not io.Reader directly) so the
// signature is self-documenting about what the helper reads from.
type pipeReader interface {
	Read(p []byte) (int, error)
}
