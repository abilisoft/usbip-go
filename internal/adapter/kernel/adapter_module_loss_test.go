//go:build linux

package kernel_test

import (
	"context"
	"io/fs"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// moduleLossBusID is the fixed busid used by every module-loss test.
// Simplifies the helper's signature to a no-arg constructor.
const moduleLossBusID = "1-1"

// allModulesFS returns a MapFS with all three kernel modules present
// plus the sysfs skeleton every method needs. Individual tests delete
// specific module dirs to simulate runtime module loss.
func allModulesFS() fstest.MapFS {
	busID := moduleLossBusID
	iface := busID + ":1.0"

	return fstest.MapFS{
		"sys/module/usbip_core": &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/usbip_host": &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/vhci_hcd":   &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host":                       &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/drivers/usbip-host/match_busid":           &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/bind":                  &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/unbind":                &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/drivers/usbip-host/rebind":                &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + busID:                         &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + busID + "/usbip_sockfd":       &fstest.MapFile{Data: []byte("")},
		"sys/bus/usb/devices/" + iface:                         &fstest.MapFile{Mode: fs.ModeDir},
		"sys/bus/usb/devices/" + iface + "/driver/driver_name": &fstest.MapFile{Data: []byte("usbhid\n")},
		"sys/devices/platform/vhci_hcd.0":        &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0/nports": &fstest.MapFile{Data: []byte("16\n")},
		"sys/devices/platform/vhci_hcd.0/status": &fstest.MapFile{Data: []byte(
			"hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 000 000 00000000 000000 0-0\n",
		)},
	}
}

// noopWriter is a WriteFunc that discards every call; the module-loss
// tests never reach a sysfs write because the preflight fails first,
// so no recording is needed.
func noopWriter() kernel.WriteFunc {
	return func(string, string) error { return nil }
}

func TestModuleLoss_Bind(t *testing.T) {
	t.Parallel()

	mfs := allModulesFS()
	delete(mfs, "sys/module/usbip_host")

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(noopWriter()),
	)
	require.NoError(t, err)

	err = a.Bind(context.Background(), domain.BusID("1-1"))
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.ErrorContains(t, err, "usbip_host")
}

func TestModuleLoss_Unbind(t *testing.T) {
	t.Parallel()

	mfs := allModulesFS()
	delete(mfs, "sys/module/usbip_host")

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(noopWriter()),
	)
	require.NoError(t, err)

	err = a.Unbind(context.Background(), domain.BusID("1-1"))
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

func TestModuleLoss_ExportOnConn(t *testing.T) {
	t.Parallel()

	mfs := allModulesFS()
	delete(mfs, "sys/module/usbip_host")

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(noopWriter()),
	)
	require.NoError(t, err)

	left, right := makeModuleLossPair(t)

	defer func() {
		_ = right.Close()
		_ = left.Close()
	}()

	err = a.ExportOnConn(context.Background(), left, domain.BusID("1-1"))
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

func TestModuleLoss_Disconnect(t *testing.T) {
	t.Parallel()

	mfs := allModulesFS()
	delete(mfs, "sys/module/usbip_host")

	a, err := kernel.NewExporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(noopWriter()),
	)
	require.NoError(t, err)

	err = a.Disconnect(context.Background(), domain.BusID("1-1"))
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

func TestModuleLoss_Attach(t *testing.T) {
	t.Parallel()

	mfs := allModulesFS()
	delete(mfs, "sys/module/vhci_hcd")

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(noopWriter()),
	)
	require.NoError(t, err)

	left, right := makeModuleLossPair(t)

	defer func() {
		_ = right.Close()
		_ = left.Close()
	}()

	spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}

	_, err = a.AttachRemote(context.Background(), left, spec)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.ErrorContains(t, err, "vhci_hcd")
}

func TestModuleLoss_Detach(t *testing.T) {
	t.Parallel()

	mfs := allModulesFS()
	delete(mfs, "sys/module/vhci_hcd")

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(noopWriter()),
	)
	require.NoError(t, err)

	err = a.DetachPort(context.Background(), domain.PortID(3))
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

func TestModuleLoss_ListPorts(t *testing.T) {
	t.Parallel()

	mfs := allModulesFS()
	delete(mfs, "sys/module/vhci_hcd")

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	ports, err := a.ListPorts(context.Background())
	require.Empty(t, ports)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.NotErrorIs(t, err, domain.ErrDeviceNotFound,
		"ListPorts must prefer ErrKernelModuleMissing over ErrDeviceNotFound")
}

func TestModuleLoss_ListLocalDevices(t *testing.T) {
	t.Parallel()

	mfs := allModulesFS()
	delete(mfs, "sys/module/usbip_core")

	a, err := kernel.NewExporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	devs, err := a.ListLocalDevices(context.Background())
	require.Empty(t, devs)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
}

// makeModuleLossPair returns a socketpair-backed net.Conn pair usable
// for tests that need a real fd. Tests close both ends themselves.
func makeModuleLossPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()

	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, pair[0], 0)
	require.GreaterOrEqual(t, pair[1], 0)

	ld, err := strconv.ParseUint(strconv.Itoa(pair[0]), 10, 64)
	require.NoError(t, err)

	rd, err := strconv.ParseUint(strconv.Itoa(pair[1]), 10, 64)
	require.NoError(t, err)

	left := os.NewFile(uintptr(ld), "pair-left")
	right := os.NewFile(uintptr(rd), "pair-right")

	lc, err := net.FileConn(left)
	require.NoError(t, err)

	rc, err := net.FileConn(right)
	require.NoError(t, err)

	_ = left.Close()
	_ = right.Close()

	return lc, rc
}

// TestModuleLoss_NetlinkCouplesToVHCITopology pins the Task-3 wiring
// contract against the pre-Task-3 spec §3.4.1 orthogonality claim. The
// dispatcher needs the vhci_hcd topology (BusMap + HCPorts) to resolve
// every uevent into the kernel's flat Port.ID — without it every
// subscriber would observe stale Port.IDs that never match the real
// status file. Subscribe therefore refuses to come up when vhci_hcd is
// not loaded (no platform/vhci_hcd.0/nports), surfacing the sysfs
// error verbatim to the caller.
//
// Orthogonality with usbip_core / usbip_host is preserved — those
// modules are never consulted by the netlink listener — but vhci_hcd
// is now load-bearing for importer-side reconnect and detach
// signalling.
func TestModuleLoss_NetlinkCouplesToVHCITopology(t *testing.T) {
	t.Parallel()

	dialer := func() (kernel.NetlinkSocket, error) {
		return &orthogonalSocket{}, nil
	}

	a, err := kernel.NewEventsAdapter(
		kernel.WithFS(fstest.MapFS{}),
		kernel.WithNetlinkDialer(dialer),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	_, _, err = a.Subscribe(ctx)
	require.Error(t, err,
		"Subscribe must fail when the VHCI topology is unavailable — "+
			"the dispatcher cannot resolve flat Port.IDs without a BusMap")
}

type orthogonalSocket struct{}

func (orthogonalSocket) Receive() ([]byte, error) { return nil, syscall.EINTR }
func (orthogonalSocket) Close() error             { return nil }
