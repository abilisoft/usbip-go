//go:build linux

package kernel

import (
	"context"
	"fmt"
	"net"
	"path"
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

	portID, err := a.findFreePort(spec.Speed)
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
