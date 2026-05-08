//go:build linux

package kernel_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
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

// errNoEmbeddedSyscall is a test-scope sentinel used when the inner
// embedded conn cannot expose a syscall.Conn. err113 rejects dynamic
// error strings in tests; this static sentinel keeps the lint green
// while documenting the failure mode.
var errNoEmbeddedSyscall = errors.New("embedded conn does not implement syscall.Conn")

// toFD narrows a positive int fd into a uintptr via a string-round-
// trip so gosec G115 sees no signed→unsigned reinterpret. Correct
// because strconv.Itoa emits an ASCII-digit string for a non-negative
// int and strconv.ParseUint interprets it verbatim.
func toFD(t *testing.T, fd int) uintptr {
	t.Helper()
	require.GreaterOrEqual(t, fd, 0)

	v, err := strconv.ParseUint(strconv.Itoa(fd), 10, 64)
	require.NoError(t, err)

	return uintptr(v)
}

// socketpairConns returns two net.Conns backed by an AF_UNIX socketpair.
// Both sides own real OS fds so syscall.Conn.SyscallConn can hand them
// back to the test for assertions on the exact fd AttachRemote passes
// to the kernel.
func socketpairConns(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()

	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, pair[0], 0, "fd[0] must be non-negative")
	require.GreaterOrEqual(t, pair[1], 0, "fd[1] must be non-negative")

	left := os.NewFile(toFD(t, pair[0]), "socketpair-left")
	right := os.NewFile(toFD(t, pair[1]), "socketpair-right")

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

// Close records the close and delegates.
func (c *closeCountingConn) Close() error {
	c.closes.Add(1)

	err := c.Conn.Close()
	if err != nil {
		return fmt.Errorf("close: %w", err)
	}

	return nil
}

// SyscallConn forwards to the embedded conn's implementation so the
// adapter's fd extraction works through the wrapper.
func (c *closeCountingConn) SyscallConn() (syscall.RawConn, error) {
	inner, ok := c.Conn.(syscall.Conn)
	if !ok {
		return nil, errNoEmbeddedSyscall
	}

	raw, err := inner.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("embedded SyscallConn: %w", err)
	}

	return raw, nil
}

// attachFS returns the minimum MapFS AttachRemote + findFreePort need:
// modules present, a status file describing one free hs port, and the
// usb* children discoverTopology requires (one HS + one SS sibling per
// controller). nports=8 on a single controller implies HCPorts=4:
// HS rows are flat 0..3 and SS rows are flat 4..7.
func attachFS() fstest.MapFS {
	return fstest.MapFS{
		"sys/module/usbip_core":                  &fstest.MapFile{Mode: fs.ModeDir},
		"sys/module/vhci_hcd":                    &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0":        &fstest.MapFile{Mode: fs.ModeDir},
		"sys/devices/platform/vhci_hcd.0/nports": &fstest.MapFile{Data: []byte("8\n")},
		"sys/devices/platform/vhci_hcd.0/status": &fstest.MapFile{Data: []byte(
			"hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 000 000 00000000 000000 0-0\n" +
				"hs  0001 000 000 00000000 000000 0-0\n" +
				"ss  0004 000 000 00000000 000000 0-0\n" +
				"ss  0005 000 000 00000000 000000 0-0\n",
		)},
		"sys/devices/platform/vhci_hcd.0/usb1/busnum": &fstest.MapFile{Data: []byte("1\n")},
		"sys/devices/platform/vhci_hcd.0/usb2/busnum": &fstest.MapFile{Data: []byte("2\n")},
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

	// All hs + ss ports busy (status 3 = USED). HS flat 0..3 and SS
	// flat 4..7 under single-controller nports=8 (HCPorts=4).
	mfs := attachFS()

	mfs["sys/devices/platform/vhci_hcd.0/status"] = &fstest.MapFile{Data: []byte(
		"hub port sta spd dev      sockfd local_busid\n" +
			"hs  0000 003 003 01020304 000005 1-1\n" +
			"hs  0001 003 003 01020304 000005 1-1\n" +
			"ss  0004 003 005 01020304 000005 1-1\n" +
			"ss  0005 003 005 01020304 000005 1-1\n",
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

// portStatusInUse is the numeric vdev_status value for a used port
// (matches the kernel's vdev_st_used enum member).
const portStatusInUse = 3

// singlePortStatus renders a vhci status table where port 0 is the
// only initially free slot; every other port starts already used.
// The WriteFunc calls markUsed after a successful attach to emulate
// the kernel's StatusNull → StatusUsed transition, so the second
// concurrent attach finds zero free ports and must return
// ErrNoFreePort.
type singlePortStatus struct {
	mu     sync.Mutex
	nports int
	// busy[i]==true means port i is already in use.
	busy []bool
}

// newSinglePortStatus builds a table of size n where only port 0 is
// free.
func newSinglePortStatus(n int) *singlePortStatus {
	busy := make([]bool, n)
	for i := 1; i < n; i++ {
		busy[i] = true
	}

	return &singlePortStatus{nports: n, busy: busy}
}

// statusText returns the current status-file bytes.
func (s *singlePortStatus) statusText() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	lines := make([]string, 0, 1+s.nports)

	lines = append(lines, "hub port sta spd dev      sockfd local_busid")

	for i := range s.nports {
		sta := 0
		if s.busy[i] {
			sta = portStatusInUse
		}

		lines = append(lines, fmt.Sprintf("hs  %04d %03d 000 00000000 000000 0-0", i, sta))
	}

	return []byte(strings.Join(lines, "\n") + "\n")
}

// markUsed commits the port as used. Called by the test's WriteFunc
// on a successful sysfs attach write.
func (s *singlePortStatus) markUsed(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if port >= 0 && port < s.nports {
		s.busy[port] = true
	}
}

// mutableStatusFS wraps fstest.MapFS so opens of the vhci status file
// return the current singlePortStatus rendering. Everything else
// passes through to the wrapped map.
type mutableStatusFS struct {
	inner fstest.MapFS
	state *singlePortStatus
}

// Open implements fs.FS.
func (m *mutableStatusFS) Open(name string) (fs.File, error) {
	if name == "sys/devices/platform/vhci_hcd.0/status" {
		fresh := fstest.MapFS{
			name: &fstest.MapFile{Data: m.state.statusText()},
		}

		f, err := fresh.Open(name)
		if err != nil {
			return nil, fmt.Errorf("mutableStatusFS open %q: %w", name, err)
		}

		return f, nil
	}

	f, err := m.inner.Open(name)
	if err != nil {
		return nil, fmt.Errorf("mutableStatusFS inner open %q: %w", name, err)
	}

	return f, nil
}

// TestFindFreePort_SSMatchesFlatBoundary pins Bug C: the
// singlePortStatus fixture must classify rows by the kernel's flat HS
// / SS boundary (HS rows at flat 0..HCPorts-1, SS rows at flat
// HCPorts..VHCIPorts-1). Pre-fix it emitted "hs" on every row
// regardless of flat index, so any SS-targeted search found zero
// matches and returned ErrNoFreePort even when a free SS slot
// existed in the kernel's actual layout.
//
// With nports=8 and one controller the kernel's HCPorts is 4: HS
// rows occupy flat 0..3 and SS rows occupy flat 4..7. The fixture
// marks only port 4 free (the first SS slot); findFreePort for
// SpeedSuper must return 4. Under the broken fixture every row is
// "hs" so SS finds nothing and errors — that is the RED condition.
func TestFindFreePort_SSMatchesFlatBoundary(t *testing.T) {
	t.Parallel()

	const (
		testNPorts      = 8
		testHCPorts     = 4 // nports / (nControllers * hubsPerController) = 8 / (1*2)
		freeSSFlatPort  = testHCPorts
	)

	state := newSinglePortStatus(testNPorts)

	// Flip the "only free" slot from port 0 (HS) to port 4 (first SS)
	// so the fixture exercises the SS classifier specifically.
	state.busy[0] = true
	state.busy[freeSSFlatPort] = false

	mfs := &mutableStatusFS{
		inner: fstest.MapFS{
			"sys/module/usbip_core":                       &fstest.MapFile{Mode: fs.ModeDir},
			"sys/module/vhci_hcd":                         &fstest.MapFile{Mode: fs.ModeDir},
			"sys/devices/platform/vhci_hcd.0":             &fstest.MapFile{Mode: fs.ModeDir},
			"sys/devices/platform/vhci_hcd.0/nports":      &fstest.MapFile{Data: fmt.Appendf(nil, "%d\n", testNPorts)},
			"sys/devices/platform/vhci_hcd.0/usb1/busnum": &fstest.MapFile{Data: []byte("1\n")},
			"sys/devices/platform/vhci_hcd.0/usb2/busnum": &fstest.MapFile{Data: []byte("2\n")},
		},
		state: state,
	}

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := kernel.FindFreePortForTest(a, domain.SpeedSuper)
	require.NoError(t, err,
		"fixture must emit an ss-labelled row at flat %d; pre-fix all rows are hs and SS finds nothing",
		freeSSFlatPort)
	require.GreaterOrEqual(t, int(got), testHCPorts,
		"SS free port must sit in the SS range (>= HCPorts)")
	require.EqualValues(t, freeSSFlatPort, got,
		"the only free SS slot is at flat %d", freeSSFlatPort)
}

// TestAttachRemote_SerializedUnderContention exercises spec §3.4:
// concurrent AttachRemote callers contending for a single free port
// must produce exactly one success and one ErrNoFreePort. Codex
// Phase 4 review finding 3.
func TestAttachRemote_SerializedUnderContention(t *testing.T) {
	t.Parallel()

	const contenderCount = 2

	left1, right1 := socketpairConns(t)
	left2, right2 := socketpairConns(t)

	defer func() {
		_ = right1.Close()
		_ = right2.Close()
	}()

	const testNPorts = 8

	state := newSinglePortStatus(testNPorts)

	mfs := &mutableStatusFS{
		inner: fstest.MapFS{
			"sys/module/usbip_core":                       &fstest.MapFile{Mode: fs.ModeDir},
			"sys/module/vhci_hcd":                         &fstest.MapFile{Mode: fs.ModeDir},
			"sys/devices/platform/vhci_hcd.0":             &fstest.MapFile{Mode: fs.ModeDir},
			"sys/devices/platform/vhci_hcd.0/nports":      &fstest.MapFile{Data: fmt.Appendf(nil, "%d\n", testNPorts)},
			"sys/devices/platform/vhci_hcd.0/usb1/busnum": &fstest.MapFile{Data: []byte("1\n")},
			"sys/devices/platform/vhci_hcd.0/usb2/busnum": &fstest.MapFile{Data: []byte("2\n")},
		},
		state: state,
	}

	writer := func(p, data string) error {
		if p != "/sys/devices/platform/vhci_hcd.0/attach" {
			return nil
		}

		var (
			port, fd int
			devID    uint32
			speed    uint32
		)

		_, err := fmt.Sscanf(data, "%d %d %d %d", &port, &fd, &devID, &speed)
		if err != nil {
			return fmt.Errorf("test writer: parse attach payload: %w", err)
		}

		state.markUsed(port)

		return nil
	}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}

	results := make([]error, contenderCount)
	conns := []net.Conn{left1, left2}

	var wg sync.WaitGroup

	wg.Add(contenderCount)

	for i := range contenderCount {
		go func(idx int) {
			defer wg.Done()

			_, attachErr := a.AttachRemote(context.Background(), conns[idx], spec)

			results[idx] = attachErr
		}(i)
	}

	wg.Wait()

	successes := 0
	noFreePortErrors := 0

	for _, r := range results {
		if r == nil {
			successes++

			continue
		}

		if errors.Is(r, domain.ErrNoFreePort) {
			noFreePortErrors++
		}
	}

	require.Equal(t, 1, successes, "exactly one Attach must succeed")
	require.Equal(t, 1, noFreePortErrors, "exactly one Attach must see ErrNoFreePort")
}
