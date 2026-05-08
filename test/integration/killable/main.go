// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Package main is the test-only subprocess that exercises
// Importer.Attach against a fake server and pauses at controllable
// checkpoints so the parent test can SIGKILL it at an exact instant.
// The binary is built on-demand by the process-death integration test;
// it is NOT a user-facing command. See test/integration/process_death_test.go
// for the parent-side orchestration that pairs with this program.
//
// The kill-point is chosen by USBIP_TEST_KILL_AT:
//
//	before_dial — pause BEFORE Transport.Dial. Parent SIGKILLs here
//	              to prove no vhci port is even attempted.
//	after_dial  — pause AFTER Dial but BEFORE AttachRemote, to cover
//	              the "kernel closes TCP socket on process death
//	              before sysfs handoff" branch (v1 contract §5.4 item 5a).
//	after_sysfs — pause AFTER a successful AttachRemote, to cover the
//	              "kernel holds its own ref, parent cleans up via
//	              Detach" branch (v1 contract §5.4 item 7).
//
// The parent reads "AT=<point>\n" from stderr to know the child is
// parked at the checkpoint; it then SIGKILLs and proceeds to assert
// the expected kernel-side state. exit code 0 when the child survives
// to the end (no kill happened) so a silent test fixture bug surfaces
// as unexpected parent-side state instead of a hung process.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// killEnv names the environment variable the parent sets to pick the
// checkpoint. Duplicated in the parent-side test via a shared
// constant; kept a string here so the helper binary has no test-side
// import dependency.
const killEnv = "USBIP_TEST_KILL_AT"

// serverEnv names the env var that carries the parent's fake-server
// address ("host:port"). The child dials this address via the real
// Importer transport so the observable effect on the kernel side is
// identical to a production attach.
const serverEnv = "USBIP_TEST_SERVER"

// busIDEnv names the env var that carries the BusID the child should
// attach. Parent uses a controlled value; child just forwards it.
const busIDEnv = "USBIP_TEST_BUSID"

// checkpointWriter is the stderr sink the parent reads synchronously.
// Using stderr keeps stdout clean for any future test that wants to
// compare child exit diagnostics.
var checkpointWriter = os.Stderr

// checkpoint names the USBIP_TEST_KILL_AT values the child understands.
type checkpoint string

const (
	checkpointBeforeDial checkpoint = "before_dial"
	checkpointAfterDial  checkpoint = "after_dial"
	checkpointAfterSysfs checkpoint = "after_sysfs"
)

// Exit codes. Named so mnd does not flag the inline literals and
// parents can map them back to failure class. exitSuccess is returned
// implicitly via `return 0` from main-run (normal termination).
const (
	exitInvalidEnv   = 2
	exitAttachFailed = 3
)

// dialProbeTimeout bounds the raw TCP dial the after_dial checkpoint
// performs before parking. A deliberately generous budget: the parent
// binds a loopback listener on a kernel-picked port, so the dial is
// effectively non-blocking on a healthy box. Three seconds is a loud
// failure if the listener never came up.
const dialProbeTimeout = 3 * time.Second

// errAddressMissingColon is the static sentinel for parseEndpoint when
// the input lacks a host:port separator. err113 requires named errors
// instead of ad-hoc errors.New at call sites.
var errAddressMissingColon = errors.New("no ':' in address")

func main() {
	os.Exit(run())
}

// run is the testable entrypoint. Separated from main so the helper
// can be unit-tested if ever needed (currently it is proven only by
// the integration parent); keeping main as a thin wrapper matches the
// pattern used across cmd/usbip and cmd/usbip.
func run() int {
	target := checkpoint(os.Getenv(killEnv))
	if target == "" {
		_, _ = fmt.Fprintln(checkpointWriter, "FATAL: USBIP_TEST_KILL_AT unset")

		return exitInvalidEnv
	}

	server := os.Getenv(serverEnv)
	if server == "" {
		_, _ = fmt.Fprintln(checkpointWriter, "FATAL: USBIP_TEST_SERVER unset")

		return exitInvalidEnv
	}

	busID := os.Getenv(busIDEnv)
	if busID == "" {
		_, _ = fmt.Fprintln(checkpointWriter, "FATAL: USBIP_TEST_BUSID unset")

		return exitInvalidEnv
	}

	endpoint, err := parseEndpoint(server)
	if err != nil {
		_, _ = fmt.Fprintf(checkpointWriter, "FATAL: parse server %q: %v\n", server, err)

		return exitInvalidEnv
	}

	// Each announceCheckpoint MUST fire BEFORE any op whose failure
	// would otherwise skip the announce and strand the parent on its
	// stderr scanner. checkpointBeforeDial already matched that shape;
	// the other two follow suit below.
	announceCheckpoint(checkpointBeforeDial)

	if target == checkpointBeforeDial {
		parkForSIGKILL()
	}

	imp, err := usbip.NewImporter()
	if err != nil {
		_, _ = fmt.Fprintf(checkpointWriter, "FATAL: NewImporter: %v\n", err)

		return exitInvalidEnv
	}

	defer func() { _ = imp.Close() }()

	if target == checkpointAfterDial {
		return runAfterDialScenario(endpoint)
	}

	// Non-target path: announce so a different scenario running the
	// whole sequence sees the line.
	announceCheckpoint(checkpointAfterDial)

	// AT=after_sysfs fires BEFORE imp.Attach so Attach failure on the
	// non-sysfs path (dial refused, protocol error, kernel handoff
	// rejected) no longer skips the announce. If Attach fails we exit
	// via exitAttachFailed; the parent, having already seen the line,
	// reaps the already-exited process instead of hanging on stderr.
	announceCheckpoint(checkpointAfterSysfs)

	_, err = imp.Attach(context.Background(), endpoint, domain.BusID(busID), usbip.AttachOptions{})
	if err != nil {
		_, _ = fmt.Fprintf(checkpointWriter, "attach error: %v\n", err)
		// Non-fatal for parent assertions; parent distinguishes
		// "attach failed as expected" from "attach succeeded".
		return exitAttachFailed
	}

	if target == checkpointAfterSysfs {
		parkForSIGKILL()
	}

	return 0
}

// runAfterDialScenario produces the "child reached a post-dial state
// with an open socket to the server" observable. Dial the parent's
// fake server first so the accept is visible, announce the checkpoint
// so the parent moves past its stderr scanner, then park so SIGKILL
// releases the socket — the test's assertPost then sees the kernel-
// driven close. A raw Dial is enough: the scenario exercises "kernel
// closes fd on process death before sysfs handoff", not the usbip
// protocol itself.
func runAfterDialScenario(endpoint domain.RemoteEndpoint) int {
	addr := net.JoinHostPort(endpoint.Host, strconv.FormatUint(uint64(endpoint.Port), 10))

	ctx, cancel := context.WithTimeout(context.Background(), dialProbeTimeout)
	defer cancel()

	var dialer net.Dialer

	probe, dialErr := dialer.DialContext(ctx, "tcp", addr)
	if dialErr != nil {
		_, _ = fmt.Fprintf(checkpointWriter, "FATAL: after_dial probe: %v\n", dialErr)

		return exitAttachFailed
	}

	_ = probe // keep the socket open; SIGKILL releases it

	announceCheckpoint(checkpointAfterDial)
	parkForSIGKILL()

	return 0
}

// parseEndpoint splits "host:port" into a RemoteEndpoint. Extracted
// so parse errors carry context; stdlib net.SplitHostPort returns an
// opaque AddrError that loses the original string.
func parseEndpoint(addr string) (domain.RemoteEndpoint, error) {
	host, portStr, err := splitHostPort(addr)
	if err != nil {
		return domain.RemoteEndpoint{}, err
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return domain.RemoteEndpoint{}, fmt.Errorf("port %q: %w", portStr, err)
	}

	return domain.RemoteEndpoint{Host: host, Port: uint16(port)}, nil
}

// splitHostPort is a minimal replica of net.SplitHostPort that keeps
// errors string-free so fmt.Errorf wraps them cleanly. IPv6 host
// parsing is not needed here — the parent always passes 127.0.0.1:NN.
func splitHostPort(addr string) (string, string, error) {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i], addr[i+1:], nil
		}
	}

	return "", "", errAddressMissingColon
}

// announceCheckpoint writes "AT=<point>\n" to stderr so the parent
// can block on a stderr scanner until the child reports its
// checkpoint. A single Write is atomic up to PIPE_BUF (4096 bytes on
// Linux) so no partial-line reads are possible.
func announceCheckpoint(c checkpoint) {
	_, _ = fmt.Fprintf(checkpointWriter, "AT=%s\n", c)
}

// parkForSIGKILL blocks forever. The parent SIGKILLs the child to
// release; there is no graceful exit from this state on purpose — a
// SIGTERM path would make the checkpoint semantics fuzzy because some
// pre-shutdown logic could reach the kernel before the signal
// delivery completes.
func parkForSIGKILL() {
	// select{} would also work but a buffered channel block makes
	// the intent clearer in a stack trace captured under gdb.
	block := make(chan struct{})
	<-block
}
