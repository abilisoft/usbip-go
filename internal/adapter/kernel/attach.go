//go:build linux

package kernel

import (
	"context"
	"fmt"
	"net"
	"path"
	"strconv"
	"syscall"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// AttachRemote performs the fd-passing dance required to hand a live
// TCP socket to vhci_hcd. Spec §5.4 pins the ordering contract; this
// method is the single source of truth for that ordering and any
// modification must re-verify the guarantees below.
//
// NOTE — fd lifecycle (spec §5.4):
//
//  1. Write first, close second. We write the sysfs `attach` file,
//     which triggers sockfd_lookup(fd) kernel-side. The kernel fgets
//     the struct file and stores it in vdev->ud.tcp_socket. This must
//     return successfully before we touch our fd. Closing earlier
//     invalidates the lookup and the attach fails.
//
//  2. Close after success. Once the sysfs write succeeds, conn.Close()
//     drops our file refcount from 2 (ours + kernel's) to 1
//     (kernel's). The socket stays alive; no FIN is sent.
//
//  3. Shutdown is a consequence. DetachPort / Disconnect signal the
//     kernel to release its ref; the underlying TCP socket then
//     emits FIN as part of normal socket teardown.
//
//  4. Failure-before-handoff cleanup. On *any* error path before the
//     sysfs write returns success, the caller still owns the conn and
//     MUST close it. This adapter NEVER closes the caller's conn on
//     error paths — the calling app method observes errors and
//     performs its own close.
//
// Violating this ordering is the most common source of regressions;
// maintainers editing this function must re-read spec §5.4 in full.
func (a *ImporterAdapter) AttachRemote(
	ctx context.Context,
	conn net.Conn,
	spec app.RemoteDeviceSpec,
) (domain.PortID, error) {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return 0, err
	}

	// Spec §3.4 serialization: findFreePort reads the status file and
	// the sysfs attach write advances it. Without this lock two
	// concurrent callers would both see the same free port and race
	// on the write. Under the lock the loser's findFreePort observes
	// the post-winner state and returns ErrNoFreePort.
	a.attachMu.Lock()
	defer a.attachMu.Unlock()

	portID, err := a.findFreePort(spec.Speed)
	if err != nil {
		return 0, err
	}

	return a.attachAtPort(ctx, conn, portID, spec)
}

// attachAtPort is the post-port-selection half of AttachRemote. It
// assumes the caller has already acquired attachMu (if any
// serialization is required) and produces the sysfs write + conn-
// lifecycle transitions for a concrete (portID, fd, devID, speed)
// tuple. The split exists so tests can drive the attach flow with an
// explicit port without routing through findFreePort; production
// always reaches this helper via AttachRemote, which picks the port
// upstream.
//
// The fd-lifecycle invariants documented on AttachRemote (spec §5.4
// write-first-close-second, caller owns conn on error) apply here
// verbatim because this helper owns the sysfs write.
//
// Defence-in-depth: the flat port is validated against the cached
// topology before any sysfs write. vhci_sysfs.c::attach_store
// returns -EINVAL when port >= nports, but surfacing that bare
// errno gives operators no context; the pre-write check wraps
// the adapter-local errPortOutOfRange with port + nports so a
// stale-cache race or a bypassed findFreePort path produces a
// diagnosable failure instead of a silent EINVAL. The check is
// cheap (one map lookup + two uint32 comparisons) and runs ahead
// of the expensive sysfs write regardless of caller.
//
// The bounds check consumes only NControllers + VHCIPorts, so it
// routes through loadStatusTopology — the BusMap-free projection
// that survives live-host mid-probe races (Task 2.1 precedent).
// Wiring attach to the full loadTopology tied every attach to
// BusMap completeness, producing spurious errTopologyIncomplete
// failures on a transient shortfall that is irrelevant to the
// bounds arithmetic.
func (a *ImporterAdapter) attachAtPort(
	_ context.Context,
	conn net.Conn,
	portID domain.PortID,
	spec app.RemoteDeviceSpec,
) (domain.PortID, error) {
	topo, err := a.loadStatusTopology()
	if err != nil {
		return 0, err
	}

	err = validateAttachPort(topo, portID)
	if err != nil {
		return 0, err
	}

	fd, err := extractFD(conn)
	if err != nil {
		return 0, err
	}

	payload := formatAttachPayload(portID, fd, spec.DevID, spec.Speed)

	err = a.writeClassified(path.Join(SysfsVHCIHCD, SysfsVHCIAttach), payload)
	if err != nil {
		// Step 4: caller owns the conn. We do NOT close it here.
		return 0, err
	}

	// Step 2: kernel holds its own ref; dropping ours is safe.
	_ = conn.Close()

	return portID, nil
}

// validateAttachPort checks that port is inside the kernel's flat
// port space [0, NControllers*VHCIPorts). vhci_sysfs.c::attach_store
// derives (pdev_nr, rhport) from the flat id and returns -EINVAL
// when pdev_nr >= VHCI_NR_HCS (our NControllers); the adapter
// surfaces that pre-write as the adapter-local errPortOutOfRange
// wrapping port + nports so a stale-cache race or bypassed
// findFreePort path produces a diagnosable failure rather than a
// bare EINVAL. The sentinel is kernel-package-local because the
// flat-port concept is VHCI-specific; pkg/domain and pkg/usbip
// must not carry kernel implementation details.
//
// VHCIPorts is guaranteed nonzero by discoverStatusTopology's nports
// validation, which loadStatusTopology routes through before
// returning a StatusTopology — so the multiplication below cannot
// overflow to zero.
//
// The signature is StatusTopology, not Topology: this validator
// consumes only the flat port arithmetic (NControllers + VHCIPorts)
// and must survive a BusMap-incomplete snapshot (mirror of the
// Task 2.1 split that moved status parsing off the full Topology).
//
// The decomposition guard is folded into the single range check:
// port < NControllers*VHCIPorts is equivalent to (controllerIdx =
// port/VHCIPorts) < NControllers, and the rhport computed as (port
// % VHCIPorts) is by construction bounded by VHCIPorts = 2*HCPorts,
// so rhport%HCPorts < HCPorts holds automatically. An explicit
// rhport>=HCPorts branch would be dead code (unreachable under the
// VHCIPorts = HCPorts*hubsPerController invariant enforced by
// deriveHCPorts); adding one would only obscure the single
// boundary actually being policed.
func validateAttachPort(topo StatusTopology, port domain.PortID) error {
	nports := topo.NControllers * topo.VHCIPorts
	if uint32(port) >= nports {
		return fmt.Errorf("%w: port=%d nports=%d", errPortOutOfRange, uint32(port), nports)
	}

	return nil
}

// extractFD walks the conn → syscall.Conn → syscall.RawConn chain to
// obtain the underlying OS file descriptor. Errors surface with enough
// context for operator diagnosis; no domain sentinel applies because
// this is a programmer contract (caller supplied a conn that does not
// expose a syscall fd — e.g. net.Pipe).
func extractFD(conn net.Conn) (uintptr, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("attach: %w (type=%T)", errConnNotSyscall, conn)
	}

	raw, err := sc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("attach: SyscallConn: %w", err)
	}

	var fd uintptr

	cerr := raw.Control(func(f uintptr) { fd = f })
	if cerr != nil {
		return 0, fmt.Errorf("attach: Control: %w", cerr)
	}

	return fd, nil
}

// formatAttachPayload renders "%u %d %u %u" = (port, sockfd, devid,
// speed). Matches upstream libsrc/vhci_driver.c for byte-for-byte
// interop. Verbatim from spec §6.1.
func formatAttachPayload(portID domain.PortID, fd uintptr, devID domain.DeviceID, speed domain.Speed) string {
	return fmt.Sprintf("%d %d %d %d", uint32(portID), fd, uint32(devID), uint32(speed))
}

// DetachPort writes the decimal port ID to vhci_hcd.0/detach. Format
// per spec §6.1: kstrtoint, single decimal integer, no trailing
// newline.
func (a *ImporterAdapter) DetachPort(ctx context.Context, id domain.PortID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	return a.writeClassified(
		path.Join(SysfsVHCIHCD, SysfsVHCIDetach),
		strconv.FormatUint(uint64(id), 10),
	)
}
