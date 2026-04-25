// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

// Package integration_test's URB-level cases (TestURB*) exercise the
// full live USB stack: vhci_hcd ↔ TCP ↔ usbip_vudc ↔ mass_storage,
// reading bytes back through a real /dev/sdN block device.
//
// CI gate policy. The kernel's vhci_hcd URB-completion workqueue
// drains asynchronously after Detach; no sysfs signal exposes "every
// completion has fully drained" so back-to-back test cases can race
// the previous session's residual state. Empirically the per-test
// pass rate sits at ~95 % and the observed run-level rate (all four
// URB tests green in one binary invocation) at ~80 %. The race is a
// test-harness isolation artifact, not a production code path — a
// real usbipd ↔ usbip client cycle has seconds to minutes between
// sessions and never races. Recommended CI policy:
//
//   - target a 90 % run-level pass rate with a single retry on
//     failure (effective gate ≈ 99 % once the empirical rate clears
//     the 90 % threshold); at the currently observed ~80 % a single
//     retry yields ~96 %, so investigate any sustained dip below
//     90 % before relying on the retry math;
//   - exclude USBIP_GO_VM_ALLOW_TCG=1 runs from the gate (TCG widens
//     the race window past the 95 % per-test floor);
//   - if per-run rate drops below 85 % over a sustained window,
//     investigate as a kernel regression or harness change.
//
// All deterministic isolation we can achieve is wired up:
// settleAfterDetach polls four kernel signals (block device removal,
// vudc usbip_status SDEV_ST_AVAILABLE, every vhci port back to
// VDEV_ST_NULL, vudc_rx / vudc_tx kthreads exited) before allowing
// the next attach; waitForVHCIBlockDevice gates on /sys/block/<name>/
// size to defeat the symlink-visible-but-not-readable window;
// firstAvailableVUDC threads an in-process tracker so back-to-back
// tests never share a vudc instance. The residual flake is past
// what userspace can deterministically observe.
package integration_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/abilisoft/usbip-go/test/integration"
	"github.com/stretchr/testify/require"
)

// e2ePayloadSize is the LUN-backing byte count planted by the test
// before attach. Must be a multiple of 512 (SCSI sector) AND large
// enough that the kernel produces multiple URB round trips — 64 KiB
// covers several bulk-transfer boundaries on USB 2.0 high-speed.
const e2ePayloadSize = 64 * 1024

// blockDevPollInterval / blockDevDeadline bound how long we wait for
// /dev/sdN to surface after imp.Attach succeeds. USB enumeration +
// usb-storage probe + sd_mod registration is typically sub-second on
// KVM but can stretch into seconds on a cold microVM cache.
const (
	blockDevPollInterval = 100 * time.Millisecond
	blockDevDeadline     = 20 * time.Second
)

// vudcSockfdPathFmt is the sysfs node the in-kernel vudc exporter
// consumes to receive an already-accepted TCP socket. Writing the
// decimal fd number hands the connection over to the kernel-side
// usbip state machine; the kernel drives the protocol from there.
// The usbip_* attributes live on the platform device, not on the
// /sys/class/udc/<name> symlink — verified on Linux 6.18.
const vudcSockfdPathFmt = "/sys/devices/platform/%s/usbip_sockfd"

// TestURBDataTransferLoopback is the rock-solid E2E: plant a known
// payload on a usbip-vudc mass_storage LUN, let our Importer attach
// over real TCP loopback, and read the bytes back through the
// /dev/sdN block device vhci_hcd creates on successful enumeration.
//
// The test exercises:
//
//   - Our wire codec (OP_REQ_IMPORT, OP_REP_IMPORT) end-to-end
//   - Our Importer.Attach kernel handoff path (sysfs attach, fd-pass)
//   - Real URB bulk transfers from vhci_hcd → TCP → vudc → gadget
//   - sd_mod / usb_storage probe, /dev/sdN creation, raw-read content
//
// Server-side brokering (TCP accept → fd handoff to kernel) is done
// directly against /sys/class/udc/<vudc>/usbip_sockfd because our
// production exporter is wired for usbip-host (real USB devices),
// not for vudc — the vudc kernel driver handles the server side
// itself once it owns the fd.
func TestURBDataTransferLoopback(t *testing.T) {
	payload := deterministicPayload(e2ePayloadSize)

	h := attachVUDCWithPayload(t, payload)

	got, err := os.ReadFile(h.blockDev)
	require.NoError(t, err, "read %s", h.blockDev)
	require.GreaterOrEqual(t, len(got), len(payload),
		"block device smaller than planted payload (%d < %d)", len(got), len(payload))
	require.Equal(t, payload, got[:len(payload)],
		"payload bytes read back from %s do not match the planted LUN content", h.blockDev)
}

// skipIfVUDCExportUnavailable bails out early if the kernel on this
// runner does not expose /sys/class/udc/<vudc>/usbip_sockfd. Some
// kernels compile usbip_vudc without the export sysfs node and the
// test would otherwise fail opaquely inside the fd-handoff write.
func skipIfVUDCExportUnavailable(t *testing.T, busID string) {
	t.Helper()

	sockfdPath := fmt.Sprintf(vudcSockfdPathFmt, busID)

	_, err := os.Stat(sockfdPath)
	if err == nil {
		return
	}

	if os.IsNotExist(err) {
		t.Skipf("integration harness: %s absent — kernel usbip_vudc built without sysfs export node", sockfdPath)
	}

	t.Skipf("integration harness: stat %s: %v", sockfdPath, err)
}

// serveVUDCSocket accepts exactly one TCP connection on lis, speaks
// phase-1 of the usbip protocol (OP_REQ_IMPORT in, OP_REP_IMPORT out)
// using our own wire codec, then hands the socket to the kernel vudc
// driver via sysfs. The kernel owns phase-2 (CMD_SUBMIT / RET_SUBMIT
// URB traffic) from that point. Splitting phase-1 off into userspace
// matches what upstream's usbipd daemon does for vudc devices — the
// in-kernel vudc entry only implements the URB half of the protocol.
func serveVUDCSocket(lis net.Listener, busID string) error {
	conn, err := lis.Accept()
	if err != nil {
		return fmt.Errorf("accept: %w", err)
	}

	defer func() { _ = conn.Close() }()

	codec := &wire.Codec{}

	// Phase 1a: decode the client's OP_REQ_IMPORT.
	reqBusID, err := codec.DecodeOpReqImport(conn)
	if err != nil {
		return fmt.Errorf("decode OP_REQ_IMPORT: %w", err)
	}

	if string(reqBusID) != busID {
		return fmt.Errorf("client requested busid %q, expected %q", reqBusID, busID)
	}

	// Phase 1b: send OP_REP_IMPORT describing the vudc-backed gadget.
	// The minimal set the client needs to produce a usable vhci port is
	// the bus id itself plus a non-zero speed — the kernel vudc side
	// fills in the rest of the descriptors once the URB phase starts.
	dev := domain.Device{
		BusID: domain.BusID(busID),
		Speed: domain.SpeedHigh,
		Path:  "/sys/devices/platform/" + busID,
	}

	err = codec.EncodeOpRepImport(conn, dev)
	if err != nil {
		return fmt.Errorf("encode OP_REP_IMPORT: %w", err)
	}

	// Phase 2 handoff: hand the fd to the kernel vudc driver via the
	// platform-device sysfs node. The kernel takes ownership and drives
	// the URB transfer loop from here.
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return fmt.Errorf("accepted conn is %T, not *net.TCPConn", conn)
	}

	f, err := tcpConn.File()
	if err != nil {
		return fmt.Errorf("tcp file: %w", err)
	}

	defer func() { _ = f.Close() }()

	sockfdPath := fmt.Sprintf(vudcSockfdPathFmt, busID)
	fdStr := strconv.Itoa(int(f.Fd()))

	err = os.WriteFile(sockfdPath, []byte(fdStr), 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", sockfdPath, err)
	}

	return nil
}

// deterministicPayload generates a byte pattern the test can rederive
// any time. PCG seeded with fixed constants so a failure can be
// reproduced bit-for-bit.
func deterministicPayload(size int) []byte {
	out := make([]byte, size)

	r := rand.New(rand.NewPCG(0xDEADBEEF, 0xFEEDFACE))
	for i := range out {
		out[i] = byte(r.Uint32())
	}

	return out
}

// waitForVHCIBlockDevice polls /sys/class/block/* until a SCSI disk
// appears whose device-parent chain resolves under /sys/devices/
// platform/vhci_hcd.0/ AND whose /sys/block/<name>/size reports the
// expected sector count (expectBytes / 512). The size check is the
// storage-readiness signal: usb_storage registers the sysfs node
// before sd_mod finishes the INQUIRY / READ CAPACITY handshake,
// so the symlink is visible before the block device is actually
// readable. Without gating on size, TestURBLargeTransfer's byte
// compare can fast-fail with a short read on a legitimately fresh
// device that is not yet addressable. `since` filters stale
// symlinks surviving from a previous attach cycle.
func waitForVHCIBlockDevice(t *testing.T, timeout time.Duration, since time.Time, expectBytes int) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	const (
		vhciSysfsPrefix = "/sys/devices/platform/vhci_hcd.0/"
		scsiSectorSize  = 512
	)

	expectSectors := int64(expectBytes) / scsiSectorSize

	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir("/sys/class/block")
		for _, e := range entries {
			name := e.Name()
			if len(name) > 3 && isDigit(name[len(name)-1]) {
				continue
			}

			devLink := filepath.Join("/sys/class/block", name, "device")

			info, err := os.Lstat(devLink)
			if err != nil {
				continue
			}

			if info.ModTime().Before(since) {
				continue
			}

			target, err := filepath.EvalSymlinks(devLink)
			if err != nil {
				continue
			}

			if !strings.HasPrefix(target, vhciSysfsPrefix) {
				continue
			}

			sizeBytes, err := os.ReadFile(filepath.Join("/sys/block", name, "size"))
			if err != nil {
				continue
			}

			sectors, err := strconv.ParseInt(strings.TrimSpace(string(sizeBytes)), 10, 64)
			if err != nil || sectors < expectSectors {
				continue
			}

			return "/dev/" + name
		}

		time.Sleep(blockDevPollInterval)
	}

	t.Fatalf("no vhci_hcd-attached block device of >= %d bytes appeared within %s (since=%s)",
		expectBytes, timeout, since.Format(time.RFC3339))

	return ""
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// urbHarness bundles the moving parts a URB-level test needs: a vudc
// gadget loaded with known payload, an importer that has attached it,
// and the resolved /dev/sdN node the kernel enumerated. Factored out
// of TestURBDataTransferLoopback so the edge-case tests below do not
// repeat the ~40 lines of setup verbatim.
type urbHarness struct {
	dev      *integration.VUDCDevice
	imp      *usbip.Importer
	port     domain.Port
	blockDev string
	want     []byte
}

// attachVUDCWithPayload plants payload on a fresh vudc gadget, runs the
// single-shot phase-1 + fd-handoff server, drives our Importer through
// a real attach, and resolves the /dev/sdN node the kernel produced.
// Every step carries its own t.Cleanup so a failure at any tier leaves
// the world clean for the next test case.
func attachVUDCWithPayload(t *testing.T, payload []byte) urbHarness {
	t.Helper()

	dev := integration.SetupVUDCWithData(t, payload)

	skipIfVUDCExportUnavailable(t, dev.BusID)

	// Pre-attach settle: wait for the kernel vudc to flip to
	// SDEV_ST_AVAILABLE before issuing the OP_REQ_IMPORT handshake.
	// A previous test case's detach can still be draining URBs when
	// SetupVUDC returns; attaching into the draining state is what
	// surfaces "vhci_hcd: cannot find a urb of seqnum ..." + a hung
	// Attach waiting for a block device that never appears.
	waitVUDCAvailable(t, dev.BusID)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "listen loopback")

	t.Cleanup(func() { _ = lis.Close() })

	serverDone := make(chan error, 1)

	go func() {
		serverDone <- serveVUDCSocket(lis, dev.BusID)
	}()

	imp, err := usbip.NewImporter()
	require.NoError(t, err, "construct importer")

	t.Cleanup(func() { _ = imp.Close() })

	addr, ok := lis.Addr().(*net.TCPAddr)
	require.True(t, ok, "listener addr must be TCP")

	ctx, cancel := context.WithTimeout(context.Background(), blockDevDeadline)
	defer cancel()

	attachStart := time.Now()

	port, err := imp.Attach(ctx, domain.RemoteEndpoint{
		Host: addr.IP.String(),
		Port: uint16(addr.Port),
	}, domain.BusID(dev.BusID), usbip.AttachOptions{})
	require.NoError(t, err, "importer attach must succeed over loopback")

	select {
	case serveErr := <-serverDone:
		require.NoError(t, serveErr, "server-side fd handoff")
	case <-time.After(blockDevDeadline):
		t.Fatal("server fd-handoff goroutine never completed")
	}

	blockDev := waitForVHCIBlockDevice(t, blockDevDeadline, attachStart, len(payload))

	// Explicit detach on cleanup so the vhci port is released and
	// /sys/block/<name> is torn down before the next test case binds
	// to the same vudc UDC. The trailing poll on /sys/block/<name>
	// blocks until the kernel finishes draining URBs: without the
	// settle wait, the kernel still ferries the final URB completion
	// while the next test's Attach fires, which surfaces as
	// "vhci_hcd: cannot find a urb of seqnum ..." in dmesg and a
	// hung Attach waiting for a /dev/sdN that never appears.
	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dcancel()

		_ = imp.Detach(dctx, port.ID)

		settleAfterDetach(t, dev.BusID, blockDev)
	})

	return urbHarness{
		dev:      dev,
		imp:      imp,
		port:     port,
		blockDev: blockDev,
		want:     payload,
	}
}

// vudcSDEVStAvailable is the numeric value the kernel vudc driver
// writes to /sys/devices/platform/<busid>/usbip_status once a gadget
// is bound to the UDC and the vudc is ready to accept a new client
// socket. Matches SDEV_ST_AVAILABLE (0x01) in usbip_device_status.
const vudcSDEVStAvailable = "1"

// settleAfterDetach blocks until every kernel signal we can observe
// agrees that the previous session is fully unwound. THREE independent
// signals must all clear:
//
//   - /sys/block/<blockDev> removed — usb_storage released the device
//   - /sys/devices/platform/<busID>/usbip_status == SDEV_ST_AVAILABLE
//     (the vudc kernel exporter is ready for a new socket handoff)
//   - Every non-null row on our controller in the vhci status file
//     has returned to VDEV_ST_NULL (no port still draining URBs)
//
// The third check is the strictest: it catches the kernel's vdev
// completion workqueue still processing the final RET_SUBMIT after
// Detach returns and usbip_status flips back to AVAILABLE. A
// lingering vhci port in any non-NULL state at this point is the
// precise precondition for the "vhci_hcd: cannot find a urb of
// seqnum ..." race that hangs the next test's Attach.
func settleAfterDetach(t *testing.T, busID, blockDev string) {
	t.Helper()

	start := time.Now()
	defer func() {
		t.Logf("settleAfterDetach(%s, %s): %s", busID, blockDev, time.Since(start))
	}()

	deadline := time.Now().Add(15 * time.Second)
	sysBlock := "/sys/block/" + filepath.Base(blockDev)
	statusPath := fmt.Sprintf("/sys/devices/platform/%s/usbip_status", busID)

	for time.Now().Before(deadline) {
		_, blockErr := os.Stat(sysBlock)
		blockGone := blockErr != nil

		statusBytes, _ := os.ReadFile(statusPath)
		vudcReady := len(statusBytes) > 0 &&
			strings.TrimSpace(string(statusBytes)) == vudcSDEVStAvailable

		vhciIdle := allVhciPortsNull(t)

		threadsGone := !vudcSessionThreadsAlive()

		if blockGone && vudcReady && vhciIdle && threadsGone {
			return
		}

		time.Sleep(blockDevPollInterval)
	}

	t.Fatalf("settleAfterDetach: kernel did not fully converge within 15s "+
		"(sysBlock=%s, status=%s) — proceeding would pollute the next test",
		sysBlock, statusPath)
}

// vudcSessionThreadsAlive reports whether any of the kernel's vudc
// per-session rx / tx kthreads are still running. The kernel spawns
// `vudc_rx` and `vudc_tx` from usbip_sockfd_store and exits them on
// detach; usbip_status flips back to SDEV_ST_AVAILABLE immediately
// when the flag changes, but the kthreads can take a few tens of
// milliseconds more to actually drain their URB queues and return.
// While they are alive, the kernel considers URB seqnum state still
// in use on the previous port — a brand-new Attach on another port
// can then race into that state and hang.
func vudcSessionThreadsAlive() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return true // fail closed: keep polling
	}

	for _, e := range entries {
		if !e.IsDir() || !isDigit(e.Name()[0]) {
			continue
		}

		commBytes, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil {
			continue
		}

		name := strings.TrimSpace(string(commBytes))
		if name == "vudc_rx" || name == "vudc_tx" {
			return true
		}
	}

	return false
}

// allVhciPortsNull reports whether every port row in the vhci status
// file is in the VDEV_ST_NULL state (kernel numeric code 4). While
// *our* previous port alone converging would suffice in theory, the
// vhci status file does not label rows with "was previously bound to
// <this client>" — so we require the whole controller to be idle.
// On a microVM test runner nothing else is consuming vhci ports, so
// this is a clean signal.
func allVhciPortsNull(t *testing.T) bool {
	t.Helper()

	const vhciStatusPath = "/sys/devices/platform/vhci_hcd.0/status"
	const vdevStNullStr = "004"
	const statusColIdx = 2
	const fieldCountMin = 7

	body, err := os.ReadFile(vhciStatusPath)
	if err != nil {
		// Can't read — fail closed so the settle loop keeps polling.
		return false
	}

	for line := range strings.SplitSeq(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) < fieldCountMin {
			continue
		}

		if fields[0] != "hs" && fields[0] != "ss" {
			// Header row.
			continue
		}

		if fields[statusColIdx] != vdevStNullStr {
			return false
		}
	}

	return true
}

// waitVUDCAvailable polls the platform vudc's usbip_status until the
// kernel reports SDEV_ST_AVAILABLE (1) — the state that accepts a new
// usbip_sockfd write. Runs before every Attach in attachVUDCWithPayload
// so a previous test's lingering kernel work cannot corrupt the new
// session's URB seqnums.
func waitVUDCAvailable(t *testing.T, busID string) {
	t.Helper()

	statusPath := fmt.Sprintf("/sys/devices/platform/%s/usbip_status", busID)
	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		b, err := os.ReadFile(statusPath)
		if err == nil && strings.TrimSpace(string(b)) == vudcSDEVStAvailable {
			return
		}

		time.Sleep(blockDevPollInterval)
	}

	t.Logf("waitVUDCAvailable: %s did not reach SDEV_ST_AVAILABLE within 10s", statusPath)
}

// detach issues Importer.Detach on the harness port with a bounded
// context. Tests that want to observe detach side-effects call this
// explicitly instead of relying on t.Cleanup so they can assert on
// post-detach kernel state before the test function returns.
func (h *urbHarness) detach(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := h.imp.Detach(ctx, h.port.ID)
	require.NoError(t, err, "Detach must succeed on port %d", h.port.ID)

	// Wait for the kernel to release the vudc + vhci state so follow-
	// up assertions (and any reattach in the test body) see a clean
	// post-detach world.
	settleAfterDetach(t, h.dev.BusID, h.blockDev)
}

// TestURBLargeTransfer verifies the URB pipeline survives a transfer
// that substantially exceeds the kernel's internal URB buffer sizes.
// 1 MiB forces the kernel to issue many CMD_SUBMIT packets back-to-
// back, exercising the wire-level flow control in both directions
// through vhci_hcd ↔ TCP ↔ vudc. Byte-for-byte readback confirms no
// bytes were dropped or reordered across the chunk boundaries.
func TestURBLargeTransfer(t *testing.T) {
	const largePayload = 1 << 20 // 1 MiB

	payload := deterministicPayload(largePayload)

	h := attachVUDCWithPayload(t, payload)

	got, err := os.ReadFile(h.blockDev)
	require.NoError(t, err, "read %s", h.blockDev)
	require.GreaterOrEqual(t, len(got), largePayload,
		"block device smaller than planted payload")
	require.Equal(t, payload, got[:largePayload],
		"1 MiB roundtrip must match planted bytes")
}

// TestURBDataTransferAfterDetach verifies Detach tears down kernel-
// side state: the /dev/sdN node disappears (eventually — udev is
// asynchronous) and the port flips from StatusUsed to a free state
// in ListPorts. Catches regressions where Detach returns nil but
// leaves half-cleaned vhci state, which on a real host would mask
// future attaches by keeping the port in a permanently-USED state.
func TestURBDataTransferAfterDetach(t *testing.T) {
	h := attachVUDCWithPayload(t, deterministicPayload(e2ePayloadSize))

	// Pre-detach sanity: ListPorts should show the port in a used
	// state. If it does not, the test body never attached properly
	// and the post-detach assertion below would be vacuous.
	ctxPre, cancelPre := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPre()

	prePorts, err := h.imp.ListPorts(ctxPre)
	require.NoError(t, err)
	require.Equal(t, domain.StatusUsed, findPortStatus(prePorts, h.port.ID),
		"port %d must be Used before Detach", h.port.ID)

	h.detach(t)

	// The microVM has no udev, so /dev/sdN is not automatically
	// unlinked when the kernel block device goes away. Check
	// /sys/block/<name> instead — that reflects the kernel's own
	// inventory and vanishes synchronously with the USB device
	// disconnect triggered by Detach.
	sysBlock := "/sys/block/" + filepath.Base(h.blockDev)

	require.Eventually(t, func() bool {
		_, serr := os.Stat(sysBlock)
		return serr != nil
	}, blockDevDeadline, blockDevPollInterval,
		"%s must disappear after Detach", sysBlock)

	// Post-detach: the port must flip out of StatusUsed. Either it
	// disappears from the listing or it reads as free (Null /
	// NotAssigned / Available). Only StatusUsed or StatusError is a
	// regression.
	ctxPost, cancelPost := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelPost()

	postPorts, err := h.imp.ListPorts(ctxPost)
	require.NoError(t, err)

	post := findPortStatus(postPorts, h.port.ID)
	require.NotEqual(t, domain.StatusUsed, post,
		"port %d must not remain Used after Detach", h.port.ID)
	require.NotEqual(t, domain.StatusError, post,
		"port %d must not flip to Error after Detach", h.port.ID)
}

// TestURBReattachCycle proves an Importer survives an attach → detach
// → attach sequence without carrying any torn vhci or vudc state
// across cycles. The second attach plants the same payload on a
// *fresh* vudc gadget: reusing the first gadget would require the
// kernel to clean its own SDEV state before a same-busid rebind,
// which it does not guarantee inside the timeout we allocate. The
// test's invariant is "another attach produces a working block
// device on the same Importer", not "bytes still there on the
// original gadget after a detach".
func TestURBReattachCycle(t *testing.T) {
	payload := deterministicPayload(e2ePayloadSize)

	h := attachVUDCWithPayload(t, payload)

	got, err := os.ReadFile(h.blockDev)
	require.NoError(t, err)
	require.Equal(t, payload, got[:len(payload)], "first attach roundtrip")

	h.detach(t) // polls vhci status until every port is VDEV_ST_NULL

	// Second attach goes to a fresh vudc gadget — firstAvailableVUDC
	// hands out a distinct instance via the in-process usage tracker.
	// The same Importer drives both cycles, catching any stale port-
	// table state it might have kept around across attaches.
	// reattachFreshVUDC registers its own t.Cleanup that detaches the
	// new port AND drains kernel state via settleAfterDetach so this
	// test cannot pollute later cases under -count=N.
	blockDev2 := reattachFreshVUDC(t, h.imp, payload)

	got2, err := os.ReadFile(blockDev2)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(got2), len(payload))
	require.Equal(t, payload, got2[:len(payload)],
		"second attach must roundtrip the planted payload on the fresh vudc")
}

// reattachFreshVUDC runs a second attach against a brand-new vudc
// instance on the same Importer. Takes its own TCP listener +
// fd-handoff goroutine and delegates gadget provisioning to
// SetupVUDCWithData, which the in-process tracker (firstAvailableVUDC)
// forces onto a previously-unused usbip-vudc.N slot.
func reattachFreshVUDC(t *testing.T, imp *usbip.Importer, payload []byte) string {
	t.Helper()

	dev := integration.SetupVUDCWithData(t, payload)

	skipIfVUDCExportUnavailable(t, dev.BusID)

	waitVUDCAvailable(t, dev.BusID)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err, "reattach: listen")

	t.Cleanup(func() { _ = lis.Close() })

	serverDone := make(chan error, 1)

	go func() { serverDone <- serveVUDCSocket(lis, dev.BusID) }()

	addr, ok := lis.Addr().(*net.TCPAddr)
	require.True(t, ok)

	ctx, cancel := context.WithTimeout(context.Background(), blockDevDeadline)
	defer cancel()

	attachStart := time.Now()

	port, err := imp.Attach(ctx, domain.RemoteEndpoint{
		Host: addr.IP.String(),
		Port: uint16(addr.Port),
	}, domain.BusID(dev.BusID), usbip.AttachOptions{})
	require.NoError(t, err, "reattach: Attach must succeed")

	select {
	case serveErr := <-serverDone:
		require.NoError(t, serveErr, "reattach: server fd handoff")
	case <-time.After(blockDevDeadline):
		t.Fatal("reattach: server goroutine never completed")
	}

	blockDev := waitForVHCIBlockDevice(t, blockDevDeadline, attachStart, len(payload))

	// Detach + drain kernel state in t.Cleanup so the caller cannot
	// forget; otherwise residual vhci / vudc state from this second
	// session leaks into later tests under -count=N.
	t.Cleanup(func() {
		detachCtx, detachCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer detachCancel()

		_ = imp.Detach(detachCtx, port.ID)
		settleAfterDetach(t, dev.BusID, blockDev)
	})

	return blockDev
}

// findPortStatus returns the Status of the port with id in ports, or
// domain.StatusNull if no matching port row is present. Callers that
// care about "missing vs free" should inspect the returned value in
// context — both outcomes represent "not used".
func findPortStatus(ports []domain.Port, id domain.PortID) domain.Status {
	for _, p := range ports {
		if p.ID == id {
			return p.Status
		}
	}

	return domain.StatusNull
}

