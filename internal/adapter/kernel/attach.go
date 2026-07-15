// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path"
	"strconv"
	"syscall"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// AttachRemote performs the fd-passing dance required to hand a live
// TCP socket to vhci_hcd. The kernel-adapter and importer-lifecycle OpenSpec documents pin the ordering contract; this
// method is the single source of truth for that ordering and any
// modification must re-verify the guarantees below.
//
// NOTE — fd lifecycle (kernel-adapter and importer-lifecycle OpenSpec documents):
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
// maintainers editing this function must re-read kernel-adapter and importer-lifecycle OpenSpec documents in full.
func (a *ImporterAdapter) AttachRemote(
	ctx context.Context,
	conn net.Conn,
	spec app.RemoteDeviceSpec,
) (domain.PortID, error) {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return 0, err
	}

	// importer-lifecycle and exporter-daemon OpenSpec documents serialization: findFreePort reads the status file and
	// the sysfs attach write advances it. Without this lock two
	// concurrent callers would both see the same free port and race
	// on the write. Under the lock the loser's findFreePort observes
	// the post-winner state and returns ErrNoFreePort.
	a.portMutationMu.Lock()
	defer a.portMutationMu.Unlock()

	// Capture one operation-local snapshot under the allocation lock. Status
	// parsing, free-port selection, and pre-write bounds validation must not mix
	// observations from two vhci_hcd module generations.
	topo, err := a.loadStatusTopology()
	if err != nil {
		return 0, err
	}

	portID, err := a.findFreePortWithTopology(topo, spec.Speed)
	if err != nil {
		return 0, err
	}

	// Publish the selected port to the importer before the potentially
	// wedged sysfs handoff. Detach can then reserve teardown intent without
	// holding the application mutex across kernel I/O. A callback failure
	// aborts before attachAtPortWithTopology can make the port live.
	if spec.ReserveLocalPort != nil {
		err = spec.ReserveLocalPort(portID)
		if err != nil {
			return 0, fmt.Errorf("reserve local port %d: %w", portID, err)
		}
	}

	return a.attachAtPortWithTopology(ctx, conn, portID, spec, topo)
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
// The fd-lifecycle invariants documented on AttachRemote (kernel-adapter and importer-lifecycle OpenSpec documents
// write-first-close-second, caller owns conn on error) apply here
// verbatim because this helper owns the sysfs write.
//
// Defence-in-depth: the flat port is validated against operation-local
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
// that survives live-host mid-probe races.
// Wiring attach to the full loadTopology tied every attach to
// BusMap completeness, producing spurious errTopologyIncomplete
// failures on a transient shortfall that is irrelevant to the
// bounds arithmetic.
// NOTE: ctx is intentionally not threaded into the sysfs writes below —
// see the matching note in Unbind. Once attachAtPort starts reading sysfs
// or writing /sys/.../vhci_hcd.0/attach the kernel offers no userspace
// cancellation hook, and aborting mid-write would leave VHCI in an
// inconsistent state (port reserved on the kernel side, no fd attached
// from ours). Callers that need a hard deadline must rely on OS-level
// process management or close the conn (the dup'd fd is closed via the
// defer below regardless of ctx state).
func (a *ImporterAdapter) attachAtPort(
	ctx context.Context,
	conn net.Conn,
	portID domain.PortID,
	spec app.RemoteDeviceSpec,
) (domain.PortID, error) {
	topo, err := a.loadStatusTopology()
	if err != nil {
		return 0, err
	}

	return a.attachAtPortWithTopology(ctx, conn, portID, spec, topo)
}

// attachAtPortWithTopology performs validation and handoff using a snapshot
// captured by the caller. AttachRemote passes the same snapshot used for status
// parsing and Port selection; the direct test helper path obtains one fresh
// snapshot in attachAtPort before delegating here.
func (a *ImporterAdapter) attachAtPortWithTopology(
	_ context.Context,
	conn net.Conn,
	portID domain.PortID,
	spec app.RemoteDeviceSpec,
	topo StatusTopology,
) (domain.PortID, error) {
	err := validatePortInRange(topo, portID)
	if err != nil {
		return 0, err
	}

	fd, err := extractFD(conn)
	if err != nil {
		return 0, err
	}

	// fd is a dup'd descriptor — close it after the kernel sysfs write
	// on all exit paths (kernel obtains its own socket ref via sockfd_lookup).
	defer func() { _ = syscall.Close(int(fd)) }()

	payload := formatAttachPayload(portID, fd, spec.DevID, spec.Speed)

	err = a.writeClassified(path.Join(SysfsVHCIHCD, SysfsVHCIAttach), payload)
	if err != nil {
		// Step 4: caller owns the conn. We do NOT close it here.
		return 0, err
	}

	// Step 2: kernel holds its own ref; dropping ours is safe.
	// A Close error here does not affect the kernel's socket reference,
	// but log it so transient fd-table issues are visible under debug.
	cerr := conn.Close()
	if cerr != nil {
		a.logger.Debug("attach: close userspace fd after kernel handoff",
			slog.Any("err", cerr))
	}

	return portID, nil
}

// validatePortInRange checks that port is inside the kernel's flat
// port space [0, NControllers*VHCIPorts). Shared between attachAtPort
// and DetachPort: vhci_sysfs.c::attach_store and ::detach_store both
// derive (pdev_nr, rhport) from the flat id and return -EINVAL when
// pdev_nr >= VHCI_NR_HCS (our NControllers); the adapter surfaces
// that pre-write as the adapter-local errPortOutOfRange wrapping
// port + nports so a stale-cache race or bypassed findFreePort path
// (attach) or a stale importer-side handle surviving a vhci_hcd
// module reload (detach) produces a diagnosable failure rather than
// a bare EINVAL. The sentinel is kernel-package-local because the
// flat-port concept is VHCI-specific; pkg/domain and pkg/usbip must
// not carry kernel implementation details.
//
// VHCIPorts is guaranteed nonzero by discoverStatusTopology's nports
// validation, which loadStatusTopology routes through before
// returning a StatusTopology — so the multiplication below cannot
// overflow to zero.
//
// The signature is StatusTopology, not Topology: this validator
// consumes only the flat port arithmetic (NControllers + VHCIPorts)
// and must survive a BusMap-incomplete snapshot (mirroring the
// split that moved status parsing off the full Topology).
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
//
// Shared rather than duplicated across attach + detach because the
// range semantics are symmetric: both kernel paths police the same
// flat index space, and a future kernel change (if any) would
// shift both in lockstep. Duplication would invite a partial
// update that left attach and detach disagreeing about the valid
// range — a silent protocol split.
func validatePortInRange(topo StatusTopology, port domain.PortID) error {
	nports := topo.NControllers * topo.VHCIPorts
	if uint32(port) >= nports {
		return fmt.Errorf("%w: port=%d nports=%d", errPortOutOfRange, uint32(port), nports)
	}

	return nil
}

// extractFD walks the conn → syscall.Conn → syscall.RawConn chain to
// obtain a dup'd OS file descriptor. The dup happens inside the Control
// callback while the Go runtime holds a reference on the underlying fd,
// preventing a concurrent conn.Close from releasing the original fd
// before the kernel sysfs write completes. Callers MUST close the
// returned fd with syscall.Close after the write (kernel takes its own
// reference via sockfd_lookup and does not depend on ours remaining open).
func extractFD(conn net.Conn) (uintptr, error) {
	sc, ok := conn.(syscall.Conn)
	if !ok {
		return 0, fmt.Errorf("attach: %w (type=%T)", errConnNotSyscall, conn)
	}

	raw, err := sc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("attach: SyscallConn: %w", err)
	}

	var (
		fd     uintptr
		dupErr error
	)

	cerr := raw.Control(func(f uintptr) {
		duped, e := syscall.Dup(int(f))
		if e != nil {
			dupErr = fmt.Errorf("dup fd: %w", e)
			return
		}

		fd = uintptr(duped)
	})
	if cerr != nil {
		return 0, fmt.Errorf("attach: Control: %w", cerr)
	}

	if dupErr != nil {
		return 0, fmt.Errorf("attach: %w", dupErr)
	}

	return fd, nil
}

// formatAttachPayload renders "%u %d %u %u" = (port, sockfd, devid,
// speed). Matches upstream libsrc/vhci_driver.c for byte-for-byte
// interop. Verbatim from kernel-adapter OpenSpec.
func formatAttachPayload(portID domain.PortID, fd uintptr, devID domain.DeviceID, speed domain.Speed) string {
	return fmt.Sprintf("%d %d %d %d", uint32(portID), fd, uint32(devID), uint32(speed))
}

// DetachPort writes the decimal port ID to vhci_hcd.0/detach. Format
// per kernel-adapter OpenSpec: kstrtoint, single decimal integer, no trailing
// newline.
//
// Defence-in-depth: the flat port is validated against the cached
// topology before any sysfs write, symmetric with attachAtPort.
// vhci_sysfs.c::detach_store returns -EINVAL when the flat port
// fails valid_port(), but surfacing that bare errno gives operators
// no context; the pre-write check wraps the adapter-local
// errPortOutOfRange with port + nports so a stale handle surviving
// a vhci_hcd module reload (or any other source of drift between
// the importer's cached portID and the current kernel port space)
// produces a diagnosable failure instead of a silent EINVAL. The
// check is cheap (one map lookup + two uint32 comparisons) and runs
// ahead of the expensive sysfs write regardless of caller.
//
// The bounds check consumes only NControllers + VHCIPorts, so it
// routes through loadStatusTopology — the BusMap-free projection
// that survives live-host mid-probe races (carrying the precedent forward,
// mirrored by attachAtPort). Using loadTopology would tie every
// detach to BusMap completeness and spuriously fail on transient
// shortfalls irrelevant to the bounds arithmetic.
func (a *ImporterAdapter) DetachPort(ctx context.Context, id domain.PortID) error {
	err := a.ModulesAvailable(ctx)
	if err != nil {
		return err
	}

	a.portMutationMu.Lock()
	defer a.portMutationMu.Unlock()

	topo, err := a.loadStatusTopology()
	if err != nil {
		return err
	}

	err = validatePortInRange(topo, id)
	if err != nil {
		return err
	}

	rows, err := a.readStatusRowsWithTopology(topo)
	if err != nil {
		return err
	}

	status, found := statusForPort(rows, id)
	if !found || isFreeStatus(status) {
		return fmt.Errorf("detach port %d: %w", id, domain.ErrDeviceNotBound)
	}

	detachErr := a.writeClassified(
		path.Join(SysfsVHCIHCD, SysfsVHCIDetach),
		formatDetachPayload(id),
	)
	if errors.Is(detachErr, syscall.EINVAL) {
		// vhci_port_disconnect returns EINVAL for a valid in-range Port whose
		// vdev is already VDEV_ST_NULL. Classify only this detach write: EINVAL
		// from topology parsing, range validation, or other sysfs operations has
		// different meaning and must remain intact.
		return fmt.Errorf(
			"detach port %d: %w (%w)", id, domain.ErrDeviceNotBound, detachErr,
		)
	}

	return detachErr
}

func statusForPort(rows []parsedPort, id domain.PortID) (domain.Status, bool) {
	for _, row := range rows {
		if row.port == id {
			return row.status, true
		}
	}

	return domain.StatusNull, false
}

// formatDetachPayload renders the kernel-flat port as a bare decimal
// integer. Matches upstream libsrc/vhci_driver.c for byte-for-byte
// interop with vhci_sysfs.c::detach_store, whose kstrtoint(buf, 10,
// &port) accepts the bare integer and tolerates (but does not
// require) a single trailing '\n'. Keeping the adapter's wire shape
// newline-free matches upstream and avoids a needless one-byte
// deviation callers might parse-test against.
func formatDetachPayload(id domain.PortID) string {
	return strconv.FormatUint(uint64(id), 10)
}
