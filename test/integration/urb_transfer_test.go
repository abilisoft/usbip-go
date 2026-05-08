//go:build integration_linux

package integration_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
	want := deterministicPayload(e2ePayloadSize)

	dev := integration.SetupVUDCWithData(t, want)

	skipIfVUDCExportUnavailable(t, dev.BusID)

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

	preSet := currentBlockDevices()

	port, err := imp.Attach(ctx, domain.RemoteEndpoint{
		Host: addr.IP.String(),
		Port: uint16(addr.Port),
	}, domain.BusID(dev.BusID), usbip.AttachOptions{})
	require.NoError(t, err, "importer attach must succeed over loopback")

	t.Cleanup(func() {
		dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dcancel()

		_ = imp.Detach(dctx, port.ID)
	})

	select {
	case serveErr := <-serverDone:
		require.NoError(t, serveErr, "server-side fd handoff")
	case <-time.After(blockDevDeadline):
		t.Fatal("server fd-handoff goroutine never completed")
	}

	blockDev := waitForNewBlockDevice(t, preSet, blockDevDeadline)

	got, err := os.ReadFile(blockDev)
	require.NoError(t, err, "read %s", blockDev)
	require.GreaterOrEqual(t, len(got), len(want),
		"block device smaller than planted payload (%d < %d)", len(got), len(want))
	require.Equal(t, want, got[:len(want)],
		"payload bytes read back from %s do not match the planted LUN content", blockDev)
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

// currentBlockDevices snapshots /dev/sd* before attach so the polling
// loop can identify the *new* node that vhci_hcd + usb_storage create
// after enumeration. sd_mod emits partitions suffixed with digits; the
// bare device itself is what we want to read.
func currentBlockDevices() map[string]struct{} {
	pre, _ := filepath.Glob("/dev/sd*")

	out := make(map[string]struct{}, len(pre))
	for _, p := range pre {
		out[p] = struct{}{}
	}

	return out
}

// waitForNewBlockDevice polls /dev/sd* until a node not in preSet
// appears. Partitions (sdaN with a trailing digit) are rejected in
// favour of the bare device so the raw-read in the test body reaches
// byte zero of the LUN backing file.
func waitForNewBlockDevice(t *testing.T, preSet map[string]struct{}, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		post, _ := filepath.Glob("/dev/sd*")
		for _, p := range post {
			if _, seen := preSet[p]; seen {
				continue
			}

			base := filepath.Base(p)
			if len(base) > 3 && isDigit(base[len(base)-1]) {
				continue
			}

			return p
		}

		time.Sleep(blockDevPollInterval)
	}

	t.Fatalf("no new /dev/sd* device appeared within %s after attach (pre=%v)", timeout, mapKeys(preSet))

	return ""
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

func mapKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

