// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/test/integration"
)

// syncBuffer is a mutex-protected bytes.Buffer. The daemon
// subprocess writes to its stdout/stderr concurrently with the test
// goroutine reading the buffer to extract the bound listener address.
// A bare bytes.Buffer is not safe under concurrent Write+String;
// `go test -race` flags the unsynchronized access.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write appends p; safe to call from the daemon writer goroutine.
func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.Write(p)
}

// String returns a snapshot of the buffered output; safe to call
// from the polling goroutine.
func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.buf.String()
}

// TestCLIFullFlow_DummyHCD exercises every CLI subcommand against a
// real dummy_hcd-backed USB device. This is the regression net the
// other suites lacked: the existing TestLoopbackAttachDetach skips
// silently when VUDC bind fails (always, on stock kernels), so prior
// to this test no path actually verified that bind/list/attach all
// succeed end-to-end against a real bindable device.
//
// Flow:
//
//  1. SetupDummyHCDGadget enumerates a dummy_hcd-backed gadget and
//     returns its busid (e.g. "1-1").
//  2. usbip-go list --output=json shows the busid in the
//     enumerated devices.
//  3. usbip-go bind <busid> succeeds.
//  4. usbip-go serve --listen :<random-port> --status-socket "" runs
//     in the background; we wait for the port to bind.
//  5. usbip-go list 127.0.0.1:<port> --output=json returns the
//     same busid.
//  6. usbip-go attach 127.0.0.1:<port> <busid> succeeds.
//  7. usbip-go port --output=json reports the attached port.
//  8. usbip-go detach <port-id> succeeds.
//  9. usbip-go unbind <busid> succeeds.
//
// Skips gracefully when the runner does not have dummy_hcd loaded.
func TestCLIFullFlow_DummyHCD(t *testing.T) {
	busID := integration.SetupDummyHCDGadget(t, "usbip_go_integration_full")

	usbipBin := integration.AbsCmdPath(t, "usbip-go")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 1: list local devices, expect our busid.
	{
		out := mustRunOK(t, ctx, usbipBin, "list", "--output=json")

		devices := parseDevicesEnvelope(t, out)

		require.True(t, jsonContainsBusID(devices, busID),
			"list must enumerate the dummy_hcd-backed gadget %q; got: %s", busID, out)
	}

	// Step 2: bind the device.
	mustRunOK(t, ctx, usbipBin, "bind", busID)

	t.Cleanup(func() {
		// Best-effort unbind even if a later step panics.
		_ = exec.Command(usbipBin, "unbind", busID).Run()
	})

	// Step 3: launch `usbip-go serve` on a kernel-picked port. --listen :0
	// hands kernel selection to the daemon so there is no TOCTOU
	// window between us closing a probe listener and the daemon
	// binding it. The daemon emits the bound address via slog at
	// info level; we read it from stderr.
	daemonCtx, daemonCancel := context.WithCancel(ctx)
	defer daemonCancel()

	daemonCmd := exec.CommandContext(
		daemonCtx, usbipBin,
		"--log-level", "info",
		"--log-format", "json",
		"serve",
		"--listen", "127.0.0.1:0",
		"--status-socket", "",
	)

	// Mutex-protected buffer because the daemon subprocess writes
	// concurrently with the polling goroutine that scans for the
	// bound-listener log line.
	var daemonOut syncBuffer

	daemonCmd.Stdout = &daemonOut
	daemonCmd.Stderr = &daemonOut

	require.NoError(t, daemonCmd.Start())

	t.Cleanup(func() {
		daemonCancel()
		_ = daemonCmd.Wait()
	})

	listenAddr := waitForDaemonListenAddr(t, &daemonOut, 5*time.Second)
	require.NoError(t, waitForListener(listenAddr, 5*time.Second),
		"usbip-go serve must accept on %s within 5s; daemon output: %s", listenAddr, daemonOut.String())

	// Step 4: list remote, expect the busid.
	{
		out := mustRunOK(t, ctx, usbipBin, "list", listenAddr, "--output=json")

		devices := parseDevicesEnvelope(t, out)

		require.True(t, jsonContainsBusID(devices, busID),
			"list HOST must return the bound busid %q; got: %s", busID, out)
	}

	// Step 5: attach the device and retain the exact port identity returned by
	// the importer. The exported busid is a remote topology identity; after a
	// same-host loopback attach, vhci_hcd enumerates a different local busid.
	attachOut := mustRunOK(t, ctx, usbipBin, "attach", listenAddr, busID, "--output=json")
	portID := parseAttachPortID(t, attachOut, busID)
	portIDArg := strconv.FormatUint(uint64(portID), 10)

	// Register the exact port cleanup IMMEDIATELY after the successful attach.
	// A fatal in the upcoming convergence check would otherwise leak the vhci
	// port until the daemon connection closes.
	detachCleanupNeeded := true
	t.Cleanup(func() {
		if detachCleanupNeeded {
			_ = exec.Command(usbipBin, "detach", portIDArg).Run()
		}
	})

	// Step 6: run `usbip-go port` until OUR acknowledged port reports the
	// used state. Matching the acknowledged id avoids both peer ports and the
	// remote-busid/local-busid distinction.
	err := waitForAttachedPortByID(
		ctx,
		commandOutput,
		usbipBin,
		portID,
		portAttachDeadline,
		portAttachPoll,
	)
	require.NoError(t, err)

	// Step 7: detach by port id.
	mustRunOK(t, ctx, usbipBin, "detach", portIDArg)
	detachCleanupNeeded = false

	// Step 8: unbind the device.
	mustRunOK(t, ctx, usbipBin, "unbind", busID)
}

// mustRunOK execs the named binary with args, returns stdout, fails
// the test on non-zero exit, and prints both streams in the failure
// message so the operator does not have to re-run the test to
// diagnose.
func mustRunOK(t *testing.T, ctx context.Context, bin string, args ...string) []byte {
	t.Helper()

	cmd := exec.CommandContext(ctx, bin, args...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	require.NoError(t, err,
		"%s %s failed: stdout=%q stderr=%q", filepath.Base(bin), args, stdout.String(), stderr.String())

	return stdout.Bytes()
}

// waitForDaemonListenAddr polls the daemon's combined stdout/stderr
// buffer for the listener-bound log line and extracts the bound
// addr. `usbip-go serve` logs an info record like
// `{"level":"INFO","msg":"usbip-go serve accepting connections","addr":"127.0.0.1:38291"}`
// once net.Listen returns and the accept loop starts; we extract
// the addr field via extractAddrFromJSONLog without depending on
// the msg text (which has changed across daemon revisions).
// Race-free alternative to the previous freeTCPPort/probe-listener
// pattern, which closed a probe listener BEFORE the daemon bound —
// opening a TOCTOU window where another process could steal the
// port.
func waitForDaemonListenAddr(t *testing.T, buf *syncBuffer, deadline time.Duration) string {
	t.Helper()

	end := time.Now().Add(deadline)

	for time.Now().Before(end) {
		addr, ok := extractAddrFromJSONLog(buf.String())
		if ok {
			return addr
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("daemon did not log a bound listener address within %s; output: %s", deadline, buf.String())

	return ""
}

// extractAddrFromJSONLog finds the `"addr":"<host:port>"` field from
// any JSON-formatted log line in s. Returns the first match; the
// daemon logs the listener addr at startup before anything else
// touches "addr".
func extractAddrFromJSONLog(s string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}

		v, ok := rec["addr"]
		if !ok {
			continue
		}

		addr, ok := v.(string)
		if !ok || addr == "" {
			continue
		}

		return addr, true
	}

	return "", false
}

// tcpPortString stringifies a TCP port without importing strconv.
// Constrained range so the format is trivial.
func tcpPortString(p int) string {
	const decBase = 10

	if p == 0 {
		return "0"
	}

	var buf [6]byte

	idx := len(buf)

	for p > 0 {
		idx--
		buf[idx] = byte('0' + p%decBase)
		p /= decBase
	}

	return string(buf[idx:])
}

// waitForListener dials addr until success or deadline. Returns nil
// on the first successful connect.
func waitForListener(addr string, deadline time.Duration) error {
	end := time.Now().Add(deadline)

	for time.Now().Before(end) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}

		time.Sleep(50 * time.Millisecond)
	}

	return os.ErrDeadlineExceeded
}

// jsonContainsBusID checks whether any device dict in the slice has
// "busid" equal to want. Used by both local and remote list
// assertions; the JSON contract puts the busid under a stable
// lowercase key per docs/json-schema.md.
func jsonContainsBusID(devices []map[string]any, want string) bool {
	for _, d := range devices {
		v, ok := d["busid"]
		if !ok {
			continue
		}

		s, ok := v.(string)
		if !ok {
			continue
		}

		if strings.TrimSpace(s) == want {
			return true
		}
	}

	return false
}

// parseDevicesEnvelope parses the {schema, devices} envelope the
// jsonRenderer emits and returns the inner devices slice. Centralised
// because the envelope is the v1 stable contract every list-flavour
// JSON output ships under (cmd/usbip-go/output.go: devicesEnvelope).
func parseDevicesEnvelope(t *testing.T, raw []byte) []map[string]any {
	t.Helper()

	var env struct {
		Schema  string           `json:"schema"`
		Devices []map[string]any `json:"devices"`
	}

	require.NoError(t, json.Unmarshal(raw, &env),
		"list --output=json must emit a valid envelope; got: %s", raw)
	require.Equal(t, "v1", env.Schema,
		"envelope.schema must be the v1 stable identifier; got: %s", raw)

	return env.Devices
}

type commandOutputFunc func(context.Context, string, ...string) ([]byte, error)

func commandOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

const (
	portAttachDeadline = 5 * time.Second
	portAttachPoll     = 50 * time.Millisecond
)

// parseAttachPortID extracts the authoritative vhci port identity from the
// successful attach acknowledgement. The acknowledgement's busid is remote,
// so it remains stable even when the local vhci topology uses another busid.
func parseAttachPortID(t *testing.T, raw []byte, wantBusID string) uint32 {
	t.Helper()

	var env struct {
		Schema string `json:"schema"`
		Op     string `json:"op"`
		OK     bool   `json:"ok"`
		Port   struct {
			ID    *uint32 `json:"id"`
			BusID string  `json:"busid"`
		} `json:"port"`
	}

	require.NoError(t, json.Unmarshal(raw, &env),
		"attach --output=json must emit a valid acknowledgement; got: %s", raw)
	require.Equal(t, "v1", env.Schema)
	require.Equal(t, "attach", env.Op)
	require.True(t, env.OK)
	require.Equal(t, wantBusID, env.Port.BusID)

	if env.Port.ID == nil {
		t.Fatalf("attach acknowledgement has no port id: %s", raw)
	}

	return *env.Port.ID
}

// waitForAttachedPortByID polls the CLI until the kernel's asynchronous VHCI
// attach becomes visible at the exact port acknowledged by Importer.Attach.
// Neither busid field participates in identity: same-host loopback changes the
// local topology, while the acknowledged numeric port remains authoritative.
func waitForAttachedPortByID(
	ctx context.Context,
	output commandOutputFunc,
	usbipBin string,
	portID uint32,
	timeout time.Duration,
	interval time.Duration,
) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var lastErr error
	var lastOutput []byte
	portIDArg := strconv.FormatUint(uint64(portID), 10)

	for {
		out, outputErr := output(ctx, usbipBin, "port", "--id", portIDArg, "--output=json")
		lastOutput = out
		if outputErr == nil {
			lastErr = validateAttachedPort(out, portID)
			if lastErr == nil {
				return nil
			}
		} else {
			lastErr = outputErr
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for VHCI port %d: %w", portID, ctx.Err())
		case <-deadline.C:
			return fmt.Errorf("VHCI port %d did not converge within %s "+
				"(last error: %v; last output: %q)",
				portID, timeout, lastErr, lastOutput)
		case <-ticker.C:
		}
	}
}

func validateAttachedPort(raw []byte, portID uint32) error {
	var env struct {
		Schema string `json:"schema"`
		Ports  []struct {
			ID     *uint32 `json:"id"`
			Status string  `json:"status"`
		} `json:"ports"`
	}

	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("decode port envelope: %w", err)
	}
	if env.Schema != "v1" {
		return fmt.Errorf("unexpected port schema %q", env.Schema)
	}
	if len(env.Ports) != 1 {
		return fmt.Errorf("port --id %d returned %d rows", portID, len(env.Ports))
	}

	port := env.Ports[0]
	if port.ID == nil {
		return errors.New("port row has no id")
	}
	if *port.ID != portID {
		return fmt.Errorf("port row id is %d, expected %d", *port.ID, portID)
	}
	if port.Status != domain.StatusUsed.String() {
		return fmt.Errorf("port %d status is %q, expected %q",
			portID, port.Status, domain.StatusUsed.String())
	}

	return nil
}
