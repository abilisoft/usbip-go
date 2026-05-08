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
//	              before sysfs handoff" branch (spec §5.4 item 5a).
//	after_sysfs — pause AFTER a successful AttachRemote, to cover the
//	              "kernel holds its own ref, parent cleans up via
//	              Detach" branch (spec §5.4 item 7).
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
	"os"
	"strconv"

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

func main() {
	os.Exit(run())
}

// run is the testable entrypoint. Separated from main so the helper
// can be unit-tested if ever needed (currently it is proven only by
// the integration parent); keeping main as a thin wrapper matches the
// pattern used across cmd/usbipd and cmd/usbip.
func run() int {
	target := checkpoint(os.Getenv(killEnv))
	if target == "" {
		fmt.Fprintln(checkpointWriter, "FATAL: USBIP_TEST_KILL_AT unset")

		return 2
	}

	server := os.Getenv(serverEnv)
	if server == "" {
		fmt.Fprintln(checkpointWriter, "FATAL: USBIP_TEST_SERVER unset")

		return 2
	}

	busID := os.Getenv(busIDEnv)
	if busID == "" {
		fmt.Fprintln(checkpointWriter, "FATAL: USBIP_TEST_BUSID unset")

		return 2
	}

	endpoint, err := parseEndpoint(server)
	if err != nil {
		fmt.Fprintf(checkpointWriter, "FATAL: parse server %q: %v\n", server, err)

		return 2
	}

	announceCheckpoint(checkpointBeforeDial)

	if target == checkpointBeforeDial {
		parkForSIGKILL()
	}

	imp, err := usbip.NewImporter()
	if err != nil {
		fmt.Fprintf(checkpointWriter, "FATAL: NewImporter: %v\n", err)

		return 2
	}

	defer func() { _ = imp.Close() }()

	announceCheckpoint(checkpointAfterDial)

	if target == checkpointAfterDial {
		// A fake-server connection is already opened inside
		// Importer.Attach's dial; for the "after_dial" checkpoint we
		// want the process to die AFTER dial but BEFORE AttachRemote.
		// Since Attach is a single call, park here and let the parent
		// SIGKILL before we enter Attach.
		parkForSIGKILL()
	}

	_, err = imp.Attach(context.Background(), endpoint, domain.BusID(busID), usbip.AttachOptions{})
	if err != nil {
		fmt.Fprintf(checkpointWriter, "attach error: %v\n", err)
		// Non-fatal for parent assertions; exit 3 so parent can
		// distinguish "attach failed as expected" from "attach
		// succeeded".
		return 3
	}

	announceCheckpoint(checkpointAfterSysfs)

	if target == checkpointAfterSysfs {
		parkForSIGKILL()
	}

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

	return "", "", errors.New("no ':' in address")
}

// announceCheckpoint writes "AT=<point>\n" to stderr so the parent
// can block on a stderr scanner until the child reports its
// checkpoint. A single Write is atomic up to PIPE_BUF (4096 bytes on
// Linux) so no partial-line reads are possible.
func announceCheckpoint(c checkpoint) {
	fmt.Fprintf(checkpointWriter, "AT=%s\n", c)
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
