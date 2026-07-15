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
// kernel state can otherwise leak across cases. This is a
// test-harness isolation hazard rather than permission to tolerate a
// failing regression: every tracked URB case must pass in the
// dedicated live-kernel environment. TCG runs remain diagnostic only
// because software emulation widens kernel scheduling windows beyond
// the KVM validation contract.
//
// All deterministic isolation we can achieve is wired up:
// settleAfterDetach polls four kernel signals (block device removal,
// vudc usbip_status SDEV_ST_AVAILABLE, every vhci port back to
// VDEV_ST_NULL, vudc_rx / vudc_tx kthreads exited) before allowing
// the next attach; waitForVHCIBlockDevice gates on /sys/block/<name>/
// size and a successful device open to defeat the
// symlink-visible-but-not-readable window; firstAvailableVUDC threads
// an in-process tracker so back-to-back tests prefer separate vudc
// instances and safely recycle a released idle instance when the
// kernel exposes only one. The residual flake is past
// what userspace can deterministically observe.
package integration_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	blockDevPollInterval  = 100 * time.Millisecond
	blockDevDeadline      = 20 * time.Second
	portOperationDeadline = 5 * time.Second
	kernelDrainDeadline   = 15 * time.Second
	vudcAvailableDeadline = 10 * time.Second
)

// vudcSockfdPathFmt is the sysfs node the in-kernel vudc exporter
// consumes to receive an already-accepted TCP socket. Writing the
// decimal fd number hands the connection over to the kernel-side
// usbip state machine; the kernel drives the protocol from there.
// The usbip_* attributes live on the platform device, not on the
// /sys/class/udc/<name> symlink — verified on Linux 6.18.
const vudcSockfdPathFmt = "/sys/devices/platform/%s/usbip_sockfd"

// The public importer accepts Linux USB topology bus IDs, while usbip_vudc
// exposes a platform-device name such as "usbip-vudc.0". The synthetic
// server therefore presents a stable, valid remote topology identity during
// the USB/IP handshake and uses the platform name only for the server-side
// usbip_sockfd handoff.
const (
	vudcRemoteBusID  = domain.BusID("1-1")
	vudcRemoteBusNum = uint16(1)
	vudcRemoteDevNum = uint16(1)
)

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
func serveVUDCSocket(lis net.Listener, remoteBusID domain.BusID, vudcBusID string) error {
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

	if reqBusID != remoteBusID {
		return fmt.Errorf("client requested busid %q, expected %q", reqBusID, remoteBusID)
	}

	// Phase 1b: send OP_REP_IMPORT describing the vudc-backed gadget.
	// The minimal set the client needs to produce a usable vhci port is
	// the bus id itself plus a non-zero speed — the kernel vudc side
	// fills in the rest of the descriptors once the URB phase starts.
	dev := domain.Device{
		BusID:  remoteBusID,
		BusNum: vudcRemoteBusNum,
		DevNum: vudcRemoteDevNum,
		Speed:  domain.SpeedHigh,
		Path:   "/sys/devices/platform/" + vudcBusID,
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

	sockfdPath := fmt.Sprintf(vudcSockfdPathFmt, vudcBusID)
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
// expected sector count (expectBytes / 512) and /dev/<name> can be
// opened. Both checks form the storage-readiness signal: usb_storage
// registers the sysfs node before sd_mod finishes the INQUIRY / READ
// CAPACITY handshake,
// so the symlink and nonzero size are visible before the block device
// necessarily accepts opens. Without both gates, a payload read can
// fast-fail while a legitimately fresh device is not yet addressable.
// `since` filters stale symlinks surviving from a previous attach cycle.
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

			blockDev := "/dev/" + name

			f, err := os.Open(blockDev)
			if err != nil {
				continue
			}

			if err := f.Close(); err != nil {
				continue
			}

			return blockDev
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
	detached *sync.Once
}

// urbAttachConfig carries the few deliberate variations in the live URB
// setup. importer is required so repeated-cycle tests can preserve one
// Importer across fresh gadgets. endpointFor may interpose a test-only TCP
// path, and afterHandoff observes the boundary after both phase-1 peers have
// transferred their sockets toward kernel ownership.
type urbAttachConfig struct {
	importer       *usbip.Importer
	endpointFor    func(t *testing.T, backend *net.TCPAddr) domain.RemoteEndpoint
	afterHandoff   func()
	discoveryLimit time.Duration
}

// attachVUDCWithPayload plants payload on a fresh vudc gadget, runs the
// single-shot phase-1 + fd-handoff server, drives our Importer through
// a real attach, and resolves the /dev/sdN node the kernel produced.
// Every step carries its own t.Cleanup so a failure at any tier leaves
// the world clean for the next test case.
func attachVUDCWithPayload(t *testing.T, payload []byte) urbHarness {
	t.Helper()

	return attachFreshVUDCWithImporter(t, newURBImporter(t), payload)
}

func newURBImporter(t *testing.T, opts ...usbip.ImporterOption) *usbip.Importer {
	t.Helper()

	imp, err := usbip.NewImporter(opts...)
	require.NoError(t, err, "construct importer")

	t.Cleanup(func() { _ = imp.Close() })

	return imp
}

// attachFreshVUDCWithImporter attaches a fresh gadget with an existing
// Importer over the direct loopback path.
func attachFreshVUDCWithImporter(
	t *testing.T,
	imp *usbip.Importer,
	payload []byte,
) urbHarness {
	t.Helper()

	return attachVUDC(t, payload, urbAttachConfig{importer: imp})
}

// attachVUDC plants payload on a fresh vudc gadget, runs the single-shot
// phase-1 + fd-handoff server, drives the configured Importer through a real
// attach, and resolves the /dev/sdN node the kernel produced. Cleanup for a
// successful Attach is registered immediately, before any later wait can
// fail, so partial setup cannot leak a live kernel attachment.
func attachVUDC(t *testing.T, payload []byte, cfg urbAttachConfig) urbHarness {
	t.Helper()
	require.NotNil(t, cfg.importer, "URB attach requires an importer")

	discoveryLimit := cfg.discoveryLimit
	if discoveryLimit == 0 {
		discoveryLimit = blockDevDeadline
	}

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
		serverDone <- serveVUDCSocket(lis, vudcRemoteBusID, dev.BusID)
	}()

	addr, ok := lis.Addr().(*net.TCPAddr)
	require.True(t, ok, "listener addr must be TCP")

	endpoint := domain.RemoteEndpoint{
		Host: addr.IP.String(),
		Port: uint16(addr.Port),
	}
	if cfg.endpointFor != nil {
		endpoint = cfg.endpointFor(t, addr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), discoveryLimit)
	defer cancel()

	attachStart := time.Now()

	port, err := cfg.importer.Attach(ctx, endpoint, vudcRemoteBusID, usbip.AttachOptions{})
	require.NoError(t, err, "importer attach must succeed over loopback")

	h := urbHarness{
		dev:      dev,
		imp:      cfg.importer,
		port:     port,
		want:     payload,
		detached: &sync.Once{},
	}

	// Register this before waiting for the server or block device. Attach has
	// already handed a socket to vhci_hcd, so either later wait can fail while
	// the kernel attachment remains live.
	t.Cleanup(func() {
		h.detached.Do(func() {
			dctx, dcancel := context.WithTimeout(context.Background(), portOperationDeadline)
			defer dcancel()

			if detachErr := h.imp.Detach(dctx, h.port.ID); detachErr != nil {
				t.Errorf("cleanup Detach on port %d: %v", h.port.ID, detachErr)
			}

			settleAfterDetach(t, h.dev.BusID, h.blockDev)
		})
	})

	select {
	case serveErr := <-serverDone:
		require.NoError(t, serveErr, "server-side fd handoff")
	case <-time.After(discoveryLimit):
		t.Fatal("server fd-handoff goroutine never completed")
	}

	if cfg.afterHandoff != nil {
		cfg.afterHandoff()
	}

	h.blockDev = waitForVHCIBlockDevice(t, discoveryLimit, attachStart, len(payload))

	return h
}

// vudcSDEVStAvailable is the numeric value the kernel vudc driver
// writes to /sys/devices/platform/<busid>/usbip_status once a gadget
// is bound to the UDC and the vudc is ready to accept a new client
// socket. Matches SDEV_ST_AVAILABLE (0x01) in usbip_device_status.
const vudcSDEVStAvailable = "1"

// settleAfterDetach blocks until every kernel signal we can observe
// agrees that the previous session is fully unwound. Four independent
// signals must all clear:
//
//   - /sys/block/<blockDev> removed — usb_storage released the device
//   - /sys/devices/platform/<busID>/usbip_status == SDEV_ST_AVAILABLE
//     (the vudc kernel exporter is ready for a new socket handoff)
//   - Every non-null row on our controller in the vhci status file
//     has returned to VDEV_ST_NULL (no port still draining URBs)
//   - The vudc_rx and vudc_tx session kthreads have exited
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

	deadline := time.Now().Add(kernelDrainDeadline)
	sysBlock := ""
	if blockDev != "" {
		sysBlock = "/sys/block/" + filepath.Base(blockDev)
	}
	statusPath := fmt.Sprintf("/sys/devices/platform/%s/usbip_status", busID)

	for time.Now().Before(deadline) {
		blockGone := sysBlock == ""
		if sysBlock != "" {
			_, blockErr := os.Stat(sysBlock)
			blockGone = statReportsMissing(blockErr)
		}

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

	t.Fatalf("settleAfterDetach: kernel did not fully converge within %s "+
		"(sysBlock=%s, status=%s) — proceeding would pollute the next test",
		kernelDrainDeadline, sysBlock, statusPath)
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

	body, err := os.ReadFile(vhciStatusPath)
	if err != nil {
		// Can't read — fail closed so the settle loop keeps polling.
		return false
	}

	return vhciStatusRowsAllNull(string(body))
}

const (
	vhciStatusFieldCount  = 7
	vhciStatusHubIndex    = 0
	vhciStatusPortIndex   = 1
	vhciStatusStateIndex  = 2
	vhciStatusSpeedIndex  = 3
	vhciStatusDevIDIndex  = 4
	vhciStatusSockFDIndex = 5
	vhciStatusBusIDIndex  = 6

	vhciStatusHubHigh  = "hs"
	vhciStatusHubSuper = "ss"
	vhciStatusHeader   = "hub"
	vhciStatusNull     = 4
	vhciStatusNoBusID  = "0-0"
	vhciStatusDecimal  = 10
	vhciStatusHex      = 16
	vhciStatusUintBits = 32
	vhciStatusFDBits   = 64
)

// vhciStatusRowsAllNull validates the complete status snapshot before
// declaring the controller idle. Empty, header-only, truncated, or malformed
// input is not evidence of release. A null row must also carry the kernel's
// zero-valued speed, device, socket, and local-BusID fields.
func vhciStatusRowsAllNull(body string) bool {
	rowCount := 0

	for line := range strings.SplitSeq(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		fields := strings.Fields(trimmed)
		if fields[vhciStatusHubIndex] == vhciStatusHeader {
			continue
		}

		if !validNullVHCIStatusRow(fields) {
			return false
		}

		rowCount++
	}

	return rowCount > 0
}

func validNullVHCIStatusRow(fields []string) bool {
	if len(fields) != vhciStatusFieldCount {
		return false
	}

	if fields[vhciStatusHubIndex] != vhciStatusHubHigh &&
		fields[vhciStatusHubIndex] != vhciStatusHubSuper {
		return false
	}

	_, portErr := strconv.ParseUint(
		fields[vhciStatusPortIndex], vhciStatusDecimal, vhciStatusUintBits,
	)
	status, statusErr := strconv.ParseUint(
		fields[vhciStatusStateIndex], vhciStatusDecimal, vhciStatusUintBits,
	)
	speed, speedErr := strconv.ParseUint(
		fields[vhciStatusSpeedIndex], vhciStatusDecimal, vhciStatusUintBits,
	)
	devID, devIDErr := strconv.ParseUint(
		fields[vhciStatusDevIDIndex], vhciStatusHex, vhciStatusUintBits,
	)
	sockFD, sockFDErr := strconv.ParseUint(
		fields[vhciStatusSockFDIndex], vhciStatusDecimal, vhciStatusFDBits,
	)

	return portErr == nil &&
		statusErr == nil && status == vhciStatusNull &&
		speedErr == nil && speed == 0 &&
		devIDErr == nil && devID == 0 &&
		sockFDErr == nil && sockFD == 0 &&
		fields[vhciStatusBusIDIndex] == vhciStatusNoBusID
}

func statReportsMissing(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// waitVUDCAvailable polls the platform vudc's usbip_status until the
// kernel reports SDEV_ST_AVAILABLE (1) — the state that accepts a new
// usbip_sockfd write. Runs before every Attach in attachVUDC
// so a previous test's lingering kernel work cannot corrupt the new
// session's URB seqnums.
func waitVUDCAvailable(t *testing.T, busID string) {
	t.Helper()

	statusPath := fmt.Sprintf("/sys/devices/platform/%s/usbip_status", busID)
	deadline := time.Now().Add(vudcAvailableDeadline)

	for time.Now().Before(deadline) {
		b, err := os.ReadFile(statusPath)
		if err == nil && strings.TrimSpace(string(b)) == vudcSDEVStAvailable {
			return
		}

		time.Sleep(blockDevPollInterval)
	}

	t.Fatalf("waitVUDCAvailable: %s did not reach SDEV_ST_AVAILABLE within %s",
		statusPath, vudcAvailableDeadline)
}

// detach issues Importer.Detach on the harness port with a bounded
// context. Tests that want to observe detach side-effects call this
// explicitly instead of relying on t.Cleanup so they can assert on
// post-detach kernel state before the test function returns.
func (h *urbHarness) detach(t *testing.T) {
	t.Helper()

	h.detached.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), portOperationDeadline)
		defer cancel()

		err := h.imp.Detach(ctx, h.port.ID)
		require.NoError(t, err, "Detach must succeed on port %d", h.port.ID)

		// Wait for the kernel to release the vudc + vhci state so follow-
		// up assertions (and any reattach in the test body) see a clean
		// post-detach world.
		settleAfterDetach(t, h.dev.BusID, h.blockDev)
	})
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
	ctxPre, cancelPre := context.WithTimeout(context.Background(), portOperationDeadline)
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
		return statReportsMissing(serr)
	}, blockDevDeadline, blockDevPollInterval,
		"%s must disappear after Detach", sysBlock)

	// Post-detach: the exact port must disappear or report a free state.
	// StatusNotAssigned is still claimed by the kernel and cannot be accepted as
	// release evidence.
	ctxPost, cancelPost := context.WithTimeout(context.Background(), portOperationDeadline)
	defer cancelPost()

	postPorts, err := h.imp.ListPorts(ctxPost)
	require.NoError(t, err)

	post := findPortStatus(postPorts, h.port.ID)
	require.True(t, releasedPortStatus(post),
		"port %d must be absent, Null, or Available after Detach; got %s",
		h.port.ID, post)
}

// lifecycleCycleCount is the smallest bounded cycle count that exercises
// more than one reattachment on the same Importer. It is regression coverage,
// not an endurance or soak-test claim.
const lifecycleCycleCount = 3

// TestURBRepeatedAttachTransferDetach proves one Importer survives multiple
// complete attachment generations without retaining stale payload, Port, or
// kernel state. Each generation uses a fresh gadget and distinct payload, then
// explicitly detaches, drains, and releases it before the next Attach.
func TestURBRepeatedAttachTransferDetach(t *testing.T) {
	imp := newURBImporter(t)

	for cycle := range lifecycleCycleCount {
		completed := false
		passed := t.Run(fmt.Sprintf("cycle-%d", cycle+1), func(t *testing.T) {
			payload := lifecyclePayload(cycle)
			h := attachFreshVUDCWithImporter(t, imp, payload)

			got, err := os.ReadFile(h.blockDev)
			require.NoError(t, err, "read %s", h.blockDev)
			require.GreaterOrEqual(t, len(got), len(payload),
				"block device smaller than planted payload")
			require.Equal(t, payload, got[:len(payload)],
				"roundtrip must match its distinct payload")

			ctxPre, cancelPre := context.WithTimeout(context.Background(), portOperationDeadline)
			prePorts, err := h.imp.ListPorts(ctxPre)
			cancelPre()
			require.NoError(t, err, "list attached ports")
			require.Equal(t, domain.StatusUsed, findPortStatus(prePorts, h.port.ID),
				"port %d must be Used before Detach", h.port.ID)

			h.detach(t)

			ctxPost, cancelPost := context.WithTimeout(context.Background(), portOperationDeadline)
			postPorts, err := h.imp.ListPorts(ctxPost)
			cancelPost()
			require.NoError(t, err, "list detached ports")

			postStatus := findPortStatus(postPorts, h.port.ID)
			require.True(t, releasedPortStatus(postStatus),
				"port %d must be absent, Null, or Available; got %s",
				h.port.ID, postStatus)

			require.NoError(t, h.dev.Release(),
				"release gadget before the next generation")
			completed = true
		})
		if !passed {
			return
		}
		if !completed {
			if cycle == 0 {
				t.Skip("cycle 1 could not acquire the live-kernel prerequisites")
			}

			t.Fatalf("cycle %d skipped after an earlier cycle proved the prerequisites", cycle+1)
		}
	}
}

func lifecyclePayload(cycle int) []byte {
	payload := deterministicPayload(e2ePayloadSize)
	payload[0] = byte(cycle)

	return payload
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

func releasedPortStatus(status domain.Status) bool {
	return status == domain.StatusNull || status == domain.StatusAvailable
}
