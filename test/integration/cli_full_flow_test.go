// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/test/integration"
)

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
//  2. usbip-go list --local --output=json shows the busid in the
//     enumerated devices.
//  3. usbip-go bind <busid> succeeds.
//  4. usbipd-go --listen :<random-port> --status-socket "" runs in
//     the background; we wait for the port to bind.
//  5. usbip-go list -r 127.0.0.1:<port> --output=json returns the
//     same busid.
//  6. usbip-go attach 127.0.0.1:<port> <busid> succeeds.
//  7. usbip-go list --ports --output=json reports the attached port.
//  8. usbip-go detach <port-id> succeeds.
//  9. usbip-go unbind <busid> succeeds.
//
// Skips gracefully when the runner does not have dummy_hcd loaded.
func TestCLIFullFlow_DummyHCD(t *testing.T) {
	busID := integration.SetupDummyHCDGadget(t, "usbip_go_integration_full")

	usbipBin := integration.AbsCmdPath(t, "usbip-go")
	usbipdBin := integration.AbsCmdPath(t, "usbipd-go")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Step 1: list local devices, expect our busid.
	{
		out := mustRunOK(t, ctx, usbipBin, "list", "--local", "--output=json")

		var devices []map[string]any
		require.NoError(t, json.Unmarshal(out, &devices),
			"list --local --output=json must emit valid JSON; got: %s", out)

		require.True(t, jsonContainsBusID(devices, busID),
			"list --local must enumerate the dummy_hcd-backed gadget %q; got: %s", busID, out)
	}

	// Step 2: bind the device.
	mustRunOK(t, ctx, usbipBin, "bind", busID)

	t.Cleanup(func() {
		// Best-effort unbind even if a later step panics.
		_ = exec.Command(usbipBin, "unbind", busID).Run()
	})

	// Step 3: launch usbipd-go on a free port.
	port := freeTCPPort(t)
	listenAddr := "127.0.0.1:" + port

	daemonCtx, daemonCancel := context.WithCancel(ctx)
	defer daemonCancel()

	daemonCmd := exec.CommandContext(daemonCtx, usbipdBin,
		"--listen", listenAddr,
		"--status-socket", "",
		"--log-level", "warn",
	)

	var daemonOut bytes.Buffer

	daemonCmd.Stdout = &daemonOut
	daemonCmd.Stderr = &daemonOut

	require.NoError(t, daemonCmd.Start())

	t.Cleanup(func() {
		daemonCancel()
		_ = daemonCmd.Wait()
	})

	require.NoError(t, waitForListener(listenAddr, 5*time.Second),
		"usbipd-go must accept on %s within 5s; daemon output: %s", listenAddr, daemonOut.String())

	// Step 4: list remote, expect the busid.
	{
		out := mustRunOK(t, ctx, usbipBin, "list", "--remote", listenAddr, "--output=json")

		var devices []map[string]any
		require.NoError(t, json.Unmarshal(out, &devices),
			"list --remote --output=json must emit valid JSON; got: %s", out)

		require.True(t, jsonContainsBusID(devices, busID),
			"list --remote must return the bound busid %q; got: %s", busID, out)
	}

	// Step 5: attach the device.
	mustRunOK(t, ctx, usbipBin, "attach", listenAddr, busID)

	t.Cleanup(func() {
		_ = exec.Command(usbipBin, "detach", "0").Run()
	})

	// Step 6: list ports — expect at least one.
	var portID string
	{
		out := mustRunOK(t, ctx, usbipBin, "list", "--ports", "--output=json")

		var ports []map[string]any
		require.NoError(t, json.Unmarshal(out, &ports),
			"list --ports --output=json must emit valid JSON; got: %s", out)
		require.NotEmpty(t, ports,
			"after attach, list --ports must report the new port; got: %s", out)

		idVal, ok := ports[0]["id"]
		require.True(t, ok, "port row must carry an id field; got: %s", out)

		portID = jsonNumber(t, idVal)
	}

	// Step 7: detach by port id.
	mustRunOK(t, ctx, usbipBin, "detach", portID)

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

// freeTCPPort returns a TCP port that was open at the moment of the
// call. The kernel may reassign the port to another process before
// the daemon actually binds, so the daemon must be tolerant of TIME_WAIT
// races. With --listen :<port> usbipd-go retries on EADDRINUSE for a
// few hundred ms before giving up, which covers this window.
func freeTCPPort(t *testing.T) string {
	t.Helper()

	var lc net.ListenConfig

	lis, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	port := lis.Addr().(*net.TCPAddr).Port

	require.NoError(t, lis.Close())

	// strconv-free formatting: TCP port is always 1-65535, no padding.
	return tcpPortString(port)
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
// "busid" equal to want. Used by both list --local and list --remote
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

// jsonNumber stringifies a JSON value that can come back as float64
// (encoding/json default) or string. The detach CLI takes a decimal
// port id so we want a plain "0", "1", … here.
func jsonNumber(t *testing.T, v any) string {
	t.Helper()

	switch n := v.(type) {
	case string:
		return n
	case float64:
		return tcpPortString(int(n))
	case json.Number:
		return n.String()
	default:
		t.Fatalf("unexpected JSON id type %T: %v", v, v)
		return ""
	}
}
