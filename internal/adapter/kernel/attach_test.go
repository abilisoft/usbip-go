//go:build linux

package kernel_test

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/internal/adapter/kernel"
	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// socketpairConns returns two net.Conns backed by an AF_UNIX socketpair.
// Both sides own real OS fds so syscall.Conn.SyscallConn can hand them
// back to the test for assertions on the exact fd AttachRemote passes
// to the kernel.
func socketpairConns(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()

	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)

	left := os.NewFile(uintptr(pair[0]), "socketpair-left")
	right := os.NewFile(uintptr(pair[1]), "socketpair-right")

	lc, err := net.FileConn(left)
	require.NoError(t, err)

	rc, err := net.FileConn(right)
	require.NoError(t, err)

	_ = left.Close()
	_ = right.Close()

	return lc, rc
}

// fdOf extracts the OS file descriptor underlying conn. The test uses
// this to know the exact fd number AttachRemote should format into its
// sysfs payload.
func fdOf(t *testing.T, conn net.Conn) uintptr {
	t.Helper()

	sc, ok := conn.(syscall.Conn)
	require.True(t, ok, "conn must satisfy syscall.Conn")

	raw, err := sc.SyscallConn()
	require.NoError(t, err)

	var fd uintptr

	cerr := raw.Control(func(f uintptr) { fd = f })
	require.NoError(t, cerr)

	return fd
}

// closeCountingConn wraps net.Conn and counts Close() invocations. The
// count is atomic so the test can inspect it after AttachRemote
// returns. It delegates SyscallConn through the embedded conn so
// AttachRemote can still extract the fd.
type closeCountingConn struct {
	net.Conn
	closes atomic.Int32
}

func (c *closeCountingConn) Close() error {
	c.closes.Add(1)

	return c.Conn.Close()
}

// SyscallConn forwards to the embedded conn's implementation so the
// adapter's fd extraction works through the wrapper.
func (c *closeCountingConn) SyscallConn() (syscall.RawConn, error) {
	inner, ok := c.Conn.(syscall.Conn)
	if !ok {
		return nil, fmt.Errorf("embedded conn missing SyscallConn")
	}

	return inner.SyscallConn()
}

// attachFS returns the minimum MapFS AttachRemote + findFreePort need:
// modules present, a status file describing one free hs port.
func attachFS() fstest.MapFS {
	return fstest.MapFS{
		"sys/module/usbip_core": &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/vhci_hcd":   &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0": &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0/nports": &fstest.MapFile{Data: []byte("8\n")},
		"sys/devices/platform/vhci_hcd.0/status": &fstest.MapFile{Data: []byte(
			"hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 004 000 00000000 000000 0-0\n" +
				"hs  0001 004 000 00000000 000000 0-0\n" +
				"ss  0002 004 000 00000000 000000 0-0\n" +
				"ss  0003 004 000 00000000 000000 0-0\n",
		)},
	}
}

func TestAttachRemote_HappyPath(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)
	defer func() { _ = right.Close() }() // peer side stays open; adapter closes left.

	wrapped := &closeCountingConn{Conn: left}
	fd := fdOf(t, left)

	var gotWrites []writeCall
	writer := func(path, data string) error {
		gotWrites = append(gotWrites, writeCall{Path: path, Data: data})

		return nil
	}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(attachFS()),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{
		DevID: domain.DeviceID(0x00010007),
		Speed: domain.SpeedHigh,
	}

	portID, err := a.AttachRemote(context.Background(), wrapped, spec)
	require.NoError(t, err)
	require.EqualValues(t, 0, portID)

	require.Len(t, gotWrites, 1)
	require.Equal(t, "/sys/devices/platform/vhci_hcd.0/attach", gotWrites[0].Path)
	require.Equal(t,
		fmt.Sprintf("%d %d %d %d", 0, fd, uint32(spec.DevID), uint32(spec.Speed)),
		gotWrites[0].Data,
	)

	require.EqualValues(t, 1, wrapped.closes.Load(),
		"conn must be closed exactly once after successful sysfs write")
}

func TestAttachRemote_FailureAtSysfsWriteDoesNotCloseConn(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)
	defer func() {
		_ = right.Close()
		_ = left.Close() // caller owns the conn on pre-handoff failure; test cleans up.
	}()

	wrapped := &closeCountingConn{Conn: left}

	writer := func(_, _ string) error {
		return unix.ENODEV
	}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(attachFS()),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}
	_, err = a.AttachRemote(context.Background(), wrapped, spec)
	require.Error(t, err)
	require.EqualValues(t, 0, wrapped.closes.Load(),
		"conn must NOT be closed when sysfs write fails")
}

func TestAttachRemote_NoFreePortDoesNotCloseConn(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)
	defer func() {
		_ = right.Close()
		_ = left.Close()
	}()

	wrapped := &closeCountingConn{Conn: left}

	// All hs + ss ports busy (status 3 = USED).
	mfs := attachFS()
	mfs["sys/devices/platform/vhci_hcd.0/status"] = &fstest.MapFile{Data: []byte(
		"hub port sta spd dev      sockfd local_busid\n" +
			"hs  0000 003 003 01020304 000005 1-1\n" +
			"hs  0001 003 003 01020304 000005 1-1\n" +
			"ss  0002 003 005 01020304 000005 1-1\n" +
			"ss  0003 003 005 01020304 000005 1-1\n",
	)}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(func(string, string) error { return nil }),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}
	_, err = a.AttachRemote(context.Background(), wrapped, spec)
	require.ErrorIs(t, err, domain.ErrNoFreePort)
	require.EqualValues(t, 0, wrapped.closes.Load(),
		"pre-handoff failure must not close the caller's conn")
}

// TestAttachRemote_ModuleMissing surfaces ErrKernelModuleMissing when
// vhci_hcd is absent — again without closing the conn.
func TestAttachRemote_ModuleMissing(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)
	defer func() {
		_ = right.Close()
		_ = left.Close()
	}()

	wrapped := &closeCountingConn{Conn: left}

	mfs := attachFS()
	delete(mfs, "sys/module/vhci_hcd")

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(func(string, string) error { return nil }),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}
	_, err = a.AttachRemote(context.Background(), wrapped, spec)
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.EqualValues(t, 0, wrapped.closes.Load())
}
