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

// TestModuleLoss_NetlinkIsOrthogonal confirms netlink Subscribe stays
// up even when modules are missing (spec §3.4.1: "Watcher goroutines
// keep running; netlink is oblivious to module presence").
func TestModuleLoss_NetlinkIsOrthogonal(t *testing.T) {
	t.Parallel()

	// EventsAdapter never reads /sys/module; the dialer is the only
	// external interaction. Inject a fake that succeeds, then confirm
	// Subscribe returns an open channel.
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

	ch, unsub, err := a.Subscribe(ctx)
	require.NoError(t, err)
	require.NotNil(t, ch)
	require.NotNil(t, unsub)

	unsub()
}

type orthogonalSocket struct{}

func (orthogonalSocket) Receive() ([]byte, error) { return nil, syscall.EINTR }
func (orthogonalSocket) Close() error             { return nil }
