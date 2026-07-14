// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

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
		testFSModuleUSBIPCorePath:       &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleVHCIHCDPath:         &fstest.MapFile{Mode: fs.ModeDir},
		testFSVHCIController0Dir:        &fstest.MapFile{Mode: fs.ModeDir},
		testFSVHCIController0NPortsPath: &fstest.MapFile{Data: []byte("8\n")},
		testFSVHCIController0StatusPath: &fstest.MapFile{Data: []byte(
			"hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 000 000 00000000 000000 0-0\n" +
				"hs  0001 000 000 00000000 000000 0-0\n" +
				"ss  0004 000 000 00000000 000000 0-0\n" +
				"ss  0005 000 000 00000000 000000 0-0\n",
		)},
		testFSVHCIController0USB1BusNumPath: &fstest.MapFile{Data: []byte("1\n")},
		testFSVHCIController0USB2BusNumPath: &fstest.MapFile{Data: []byte("2\n")},
	}
}

// failSecondStatusOpenFS lets topology discovery observe the controller's
// status file, then fails the operation's subsequent row read. This isolates
// DetachPort's post-topology error path from the earlier controller probe.
type failSecondStatusOpenFS struct {
	inner fs.FS
	opens atomic.Int32
}

func (f *failSecondStatusOpenFS) Open(name string) (fs.File, error) {
	if name == testFSVHCIController0StatusPath && f.opens.Add(1) == 2 {
		return nil, &fs.PathError{
			Op:   testOpenOperation,
			Path: name,
			Err:  fs.ErrPermission,
		}
	}

	file, err := f.inner.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}

	return file, nil
}

// rotatingNPortsFS returns successive nports values on successive opens while
// delegating every other path. It deterministically models a vhci_hcd reload
// between two topology reads without mutating shared filesystem state.
type rotatingNPortsFS struct {
	inner  fs.FS
	values []string

	mu    sync.Mutex
	opens int
}

func (f *rotatingNPortsFS) Open(name string) (fs.File, error) {
	if name != testFSVHCIController0NPortsPath {
		file, err := f.inner.Open(name)
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", name, err)
		}

		return file, nil
	}

	f.mu.Lock()
	idx := min(f.opens, len(f.values)-1)
	value := f.values[idx]
	f.opens++
	f.mu.Unlock()

	file, err := (fstest.MapFS{
		name: &fstest.MapFile{Data: []byte(value)},
	}).Open(name)
	if err != nil {
		return nil, fmt.Errorf("open rotating nports %s: %w", name, err)
	}

	return file, nil
}

func (f *rotatingNPortsFS) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.opens
}

// TestAttachRemote_MalformedNPortsStopsBeforeReservationAndHandoff proves a
// topology parse failure returns the zero PortID before any importer
// reservation, sysfs write, or connection ownership transition.
func TestAttachRemote_MalformedNPortsStopsBeforeReservationAndHandoff(t *testing.T) {
	t.Parallel()

	left, right := net.Pipe()
	defer func() {
		_ = right.Close()
		_ = left.Close()
	}()

	wrapped := &closeCountingConn{Conn: left}
	mfs := attachFS()

	mfs[testFSVHCIController0NPortsPath] = &fstest.MapFile{Data: []byte("not-a-port-count\n")}

	var (
		reservations atomic.Int32
		writes       atomic.Int32
	)

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(func(string, string) error {
			writes.Add(1)

			return nil
		}),
	)
	require.NoError(t, err)

	port, err := a.AttachRemote(context.Background(), wrapped, app.RemoteDeviceSpec{
		DevID: 1,
		Speed: domain.SpeedHigh,
		ReserveLocalPort: func(domain.PortID) error {
			reservations.Add(1)

			return nil
		},
	})
	require.ErrorIs(t, err, strconv.ErrSyntax)
	require.ErrorContains(t, err, "vhci_hcd.0/nports")
	require.Zero(t, port)
	require.Zero(t, reservations.Load(), "topology failure must precede port reservation")
	require.Zero(t, writes.Load(), "topology failure must precede the sysfs handoff")
	require.Zero(t, wrapped.closes.Load(), "pre-handoff failure leaves connection ownership with the caller")
}

// TestAttachRemote_UsesOneStatusTopologySnapshot proves free-port selection
// and pre-write validation cannot mix module generations. Port 15 is valid in
// the first 16-port snapshot but invalid in the second 8-port snapshot; a
// second topology read would therefore fail before the write.
func TestAttachRemote_UsesOneStatusTopologySnapshot(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)
	defer func() {
		_ = right.Close()
		_ = left.Close()
	}()

	wrapped := &closeCountingConn{Conn: left}
	mfs := attachFS()

	mfs[testFSVHCIController0NPortsPath] = &fstest.MapFile{Data: []byte("16\n")}
	mfs[testFSVHCIController0StatusPath] = &fstest.MapFile{Data: []byte(
		"hub port sta spd dev      sockfd local_busid\n" +
			"ss  0015 000 005 00000000 000000 0-0\n",
	)}

	rotating := &rotatingNPortsFS{
		inner:  mfs,
		values: []string{"16\n", "8\n"},
	}

	var writes atomic.Int32

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(rotating),
		kernel.WithWriteFunc(func(path, _ string) error {
			require.Equal(t, testVHCIAttachPath, path)
			writes.Add(1)

			return nil
		}),
	)
	require.NoError(t, err)

	port, err := a.AttachRemote(context.Background(), wrapped, app.RemoteDeviceSpec{
		DevID: 1,
		Speed: domain.SpeedSuper,
	})
	require.NoError(t, err)
	require.Equal(t, domain.PortID(15), port)
	require.Equal(t, 1, rotating.openCount(),
		"one AttachRemote call must load status topology exactly once")
	require.EqualValues(t, 1, writes.Load())
}

// TestAttachRemote_PortOutOfRangeSentinelIsAdapterLocal pins the
// layering invariant: the out-of-range sentinel lives in
// the kernel adapter package, not on pkg/domain or pkg/usbip. The
// sentinel is VHCI-specific — no other kernel HCD surfaces it —
// so exposing it on the domain or public facade would enlarge the
// semver surface with a kernel implementation detail. White-box
// tests reach the sentinel via the ErrPortOutOfRangeForTest shim;
// public consumers see a wrapped fmt.Errorf they can read for
// context but not programmatically classify (operator error at
// worst, and the pre-write boundary is unreachable by production
// callers that go through findFreePort).
//
// Before this commit: only domain.ErrPortOutOfRange and the public
// re-export satisfy errors.Is; no adapter-local shim exists.
// After: kernel.ErrPortOutOfRangeForTest exposes the adapter's
// internal sentinel, domain + usbip entries are gone, and the
// attach path wraps the adapter-local sentinel.
func TestAttachRemote_PortOutOfRangeSentinelIsAdapterLocal(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)

	defer func() {
		_ = right.Close()
		_ = left.Close()
	}()

	wrapped := &closeCountingConn{Conn: left}

	writer := func(string, string) error { return nil }

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(attachFS()),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}

	_, err = kernel.AttachAtPortForTest(context.Background(), a, wrapped, domain.PortID(20), spec)
	require.ErrorIs(t, err, kernel.ErrPortOutOfRangeForTest,
		"adapter-local sentinel must be reachable through the white-box shim")
}

// TestAttachRemote_RejectsOutOfRangePort pins the defence-in-
// depth bounds check: a flat port identifier outside the kernel's
// port space must be refused by the adapter before any sysfs write.
// vhci_sysfs.c::attach_store returns -EINVAL when `port >= nports`,
// but surfacing that bare errno gives operators no context; the
// adapter therefore validates proactively and wraps the adapter-
// local errPortOutOfRange sentinel with port + nports context.
//
// validateAttachPort short-circuits before writeClassified, the write
// spy records zero calls, and errors.Is surfaces the adapter-local
// sentinel via its test shim. A regression where attachAtPort wrote
// the payload regardless of port range would let the spy record one
// call and the adapter return nil.
//
// attachFS() has nports=8, so port 20 is well outside [0, 8).
// AttachAtPortForTest drives the post-selection attach path
// directly with the out-of-range port; findFreePort upstream
// cannot emit such a value (parseStatusFile already rejects rows
// outside the controller window), but the bounds check hardens the
// path against stale caches or future bypass callers — the exact
// hole closes.
func TestAttachRemote_RejectsOutOfRangePort(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)

	defer func() {
		_ = right.Close()
		_ = left.Close() // caller owns conn on pre-handoff failure; test cleans up.
	}()

	wrapped := &closeCountingConn{Conn: left}

	var writes atomic.Int32

	writer := func(string, string) error {
		writes.Add(1)

		return nil
	}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(attachFS()),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}

	// attachFS() declares nports=8; port 20 is well past the window.
	_, err = kernel.AttachAtPortForTest(context.Background(), a, wrapped, domain.PortID(20), spec)
	require.ErrorIs(t, err, kernel.ErrPortOutOfRangeForTest,
		"flat port 20 is outside [0, 8); must surface errPortOutOfRange")
	require.EqualValues(t, 0, writes.Load(),
		"bounds check must refuse the port BEFORE any sysfs write")
	require.EqualValues(t, 0, wrapped.closes.Load(),
		"pre-handoff failure must not close the caller's conn")
}

// TestAttachRemote_SucceedsDespiteIncompleteBusMap pins the Task
// 4.1 layering invariant: the attach bounds-check path consumes
// only StatusTopology (NControllers + VHCIPorts), never the
// BusMap. A
// controller mid-probe whose usb* children are not yet populated in
// sysfs produces an incomplete BusMap — discoverTopology rejects
// that snapshot with errTopologyIncomplete. Routing attachAtPort
// through the full loadTopology tied every attach to that
// completeness check, so an attach with a perfectly in-range port
// spuriously failed on a transient BusMap shortfall.
//
// attachAtPort validates via loadStatusTopology (mirrors
// readStatusRows's split). A fixture with nports=8 and no
// usb* children still exposes enough topology for the bounds check;
// port 0 (in range under [0, 8)) must attach successfully.
//
// Routing attach through loadTopology would surface
// errTopologyIncomplete from the BusMap completeness check whenever
// vhci_hcd.0 had zero usb children (hubsPerController=2 expected).
func TestAttachRemote_SucceedsDespiteIncompleteBusMap(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)

	defer func() { _ = right.Close() }() // adapter closes left on success.

	wrapped := &closeCountingConn{Conn: left}

	var writes atomic.Int32

	writer := func(string, string) error {
		writes.Add(1)

		return nil
	}

	// Minimal fixture: modules present, valid nports + status file,
	// but NO usb* children under vhci_hcd.0. discoverTopology would
	// fail with errTopologyIncomplete; discoverStatusTopology must
	// succeed because it never walks usb* children.
	mfs := fstest.MapFS{
		testFSModuleUSBIPCorePath:       &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleVHCIHCDPath:         &fstest.MapFile{Mode: fs.ModeDir},
		testFSVHCIController0Dir:        &fstest.MapFile{Mode: fs.ModeDir},
		testFSVHCIController0NPortsPath: &fstest.MapFile{Data: []byte("8\n")},
		testFSVHCIController0StatusPath: &fstest.MapFile{Data: []byte(
			"hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 000 000 00000000 000000 0-0\n",
		)},
	}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}

	_, err = kernel.AttachAtPortForTest(context.Background(), a, wrapped, domain.PortID(0), spec)
	require.NoError(t, err,
		"attach bounds validation must not depend on BusMap completeness; "+
			"routing through loadTopology would return errTopologyIncomplete")
	require.EqualValues(t, 1, writes.Load(),
		"valid in-range port must reach sysfs write exactly once")
	require.EqualValues(t, 1, wrapped.closes.Load(),
		"conn must be closed exactly once after successful attach")
}

// TestAttachRemote_RejectsPortAtNportsBoundary pins the exact
// boundary semantics of the validator: nports itself is the first
// invalid identifier (the valid range is half-open [0, nports));
// an off-by-one making the range inclusive would corrupt the very
// top slot. attachFS() declares nports=8, so port 8 must be
// rejected and port 7 (the last valid slot) must succeed.
func TestAttachRemote_RejectsPortAtNportsBoundary(t *testing.T) {
	t.Parallel()

	// Reject: port == nports. findFreePort's last valid return under
	// nports=8 is 7; port 8 is the first invalid slot.
	t.Run("at_nports_is_rejected", func(t *testing.T) {
		t.Parallel()

		left, right := socketpairConns(t)

		defer func() {
			_ = right.Close()
			_ = left.Close()
		}()

		wrapped := &closeCountingConn{Conn: left}

		var writes atomic.Int32

		writer := func(string, string) error {
			writes.Add(1)

			return nil
		}

		a, err := kernel.NewImporterAdapter(
			kernel.WithFS(attachFS()),
			kernel.WithWriteFunc(writer),
		)
		require.NoError(t, err)

		spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}

		_, err = kernel.AttachAtPortForTest(context.Background(), a, wrapped, domain.PortID(8), spec)
		require.ErrorIs(t, err, kernel.ErrPortOutOfRangeForTest,
			"flat port 8 is the off-by-one boundary — range is [0, 8)")
		require.EqualValues(t, 0, writes.Load(),
			"port == nports must not reach sysfs write")
	})

	// Accept: port == nports-1, the last valid identifier. This
	// guards against an off-by-one in the validator that would
	// reject the top slot.
	t.Run("nports_minus_one_is_accepted", func(t *testing.T) {
		t.Parallel()

		left, right := socketpairConns(t)

		defer func() { _ = right.Close() }() // adapter closes left on success.

		wrapped := &closeCountingConn{Conn: left}

		var writes atomic.Int32

		writer := func(string, string) error {
			writes.Add(1)

			return nil
		}

		a, err := kernel.NewImporterAdapter(
			kernel.WithFS(attachFS()),
			kernel.WithWriteFunc(writer),
		)
		require.NoError(t, err)

		spec := app.RemoteDeviceSpec{DevID: 1, Speed: domain.SpeedHigh}

		got, err := kernel.AttachAtPortForTest(context.Background(), a, wrapped, domain.PortID(7), spec)
		require.NoError(t, err,
			"flat port 7 (nports-1=7) is the last valid slot; validator must accept it")
		require.EqualValues(t, 7, got)
		require.EqualValues(t, 1, writes.Load(),
			"valid port must reach sysfs write exactly once")
		require.EqualValues(t, 1, wrapped.closes.Load(),
			"conn must be closed exactly once after successful attach")
	})
}

// TestFormatAttachPayload_FieldOrderingMatchesKernel pins the exact
// byte-for-byte shape the adapter writes to vhci_hcd's attach sysfs
// node. Kernel 7.0's vhci_sysfs.c::attach_store consumes the write
// with:
//
//	sscanf(buf, "%u %u %u %u", &port, &sockfd, &devid, &speed)
//
// Any reordering of those four fields — or any insertion/removal of
// separators — would compile fine but silently attach the wrong
// device at the wrong port. sscanf tolerates (but does not require) a
// trailing newline; upstream userspace (libsrc/vhci_driver.c) does
// not append one and this adapter matches that wire shape exactly.
//
// This is a RED against future drift: any edit to formatAttachPayload
// that reorders, renames, or re-separates fields must update this
// test deliberately.
func TestFormatAttachPayload_FieldOrderingMatchesKernel(t *testing.T) {
	t.Parallel()

	const (
		port   = domain.PortID(5)
		fd     = uintptr(42)
		devID  = domain.DeviceID(0x01020304)
		speed  = domain.SpeedHigh
		expect = "5 42 16909060 3"
	)

	got := kernel.FormatAttachPayloadForTest(port, fd, devID, speed)
	require.Equal(t, expect, got,
		"attach payload must render as '<port> <sockfd> <devid> <speed>' — "+
			"sscanf(\"%%u %%u %%u %%u\") order in vhci_sysfs.c::attach_store")
}

// TestFormatDetachPayload_MatchesKernelContract pins the exact byte-
// for-byte shape the adapter writes to vhci_hcd's detach sysfs node.
// Kernel 6.x vhci_sysfs.c::detach_store consumes the write with:
//
//	kstrtoint(buf, 10, &port)
//
// kstrtoint accepts a bare decimal integer and tolerates a single
// optional trailing '\n'. Upstream libsrc/vhci_driver.c writes the
// decimal integer without a newline; this adapter matches that wire
// shape exactly so any drift toward "5\n" (harmless to the kernel but
// a deliberate wire change) or "05", " 5 ", "+5", hex, or any other
// format is caught immediately.
//
// Parallel to TestFormatAttachPayload_FieldOrderingMatchesKernel: both
// tests isolate the payload formatter from the sysfs path + module
// availability plumbing so a single edit to the byte shape surfaces
// here, not in a tangled AttachRemote/DetachPort happy-path assertion.
//
// This is a RED against future drift: any edit to formatDetachPayload
// that changes the rendering must update this test deliberately.
func TestFormatDetachPayload_MatchesKernelContract(t *testing.T) {
	t.Parallel()

	const (
		port   = domain.PortID(5)
		expect = "5"
	)

	got := kernel.FormatDetachPayloadForTest(port)
	require.Equal(t, expect, got,
		"detach payload must render as a bare decimal integer (no newline, "+
			"no leading zeros, no sign) — kstrtoint(buf, 10, &port) in "+
			"vhci_sysfs.c::detach_store")
}

// TestDetachPort_RejectsOutOfRangePort pins symmetry with 's
// attach bounds check: a detach request targeting a flat port outside
// the kernel's port space must be refused by the adapter before any
// sysfs write. vhci_sysfs.c::detach_store runs kstrtoint + valid_port
// and returns -EINVAL when pdev_nr >= VHCI_NR_HCS (our NControllers);
// surfacing that bare errno gives operators no context. The adapter
// therefore validates proactively and wraps the adapter-local
// errPortOutOfRange sentinel with port + nports context, matching the
// attach flow byte for byte.
//
// Without the check a stale portID (cached by the importer across a
// vhci_hcd module reload) would silently fall through to writeClassified
// and fail with a bare EINVAL the operator cannot classify.
//
// validateDetachPort short-circuits before writeClassified, the write
// spy records zero calls, and errors.Is surfaces the adapter-local
// sentinel via its test shim. If DetachPort ignored port range the
// spy would record one call and the adapter would return nil.
//
// attachFS() has nports=8, so port 20 is well outside [0, 8).
func TestDetachPort_RejectsOutOfRangePort(t *testing.T) {
	t.Parallel()

	var writes atomic.Int32

	writer := func(string, string) error {
		writes.Add(1)

		return nil
	}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(attachFS()),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	err = a.DetachPort(context.Background(), domain.PortID(20))
	require.ErrorIs(t, err, kernel.ErrPortOutOfRangeForTest,
		"flat port 20 is outside [0, 8); must surface errPortOutOfRange")
	require.EqualValues(t, 0, writes.Load(),
		"bounds check must refuse the port BEFORE any sysfs write")
}

// TestDetachPort_AcceptsInRangePort regresses the common path: a
// detach targeting a valid flat port must still reach the sysfs write
// exactly once and render the expected decimal payload. Guards against
// a validator that over-rejects — e.g. an off-by-one making the range
// exclusive of its upper neighbour, or a swap of < / <= that refuses
// port 0. Mirrors TestAttachRemote_RejectsPortAtNportsBoundary's
// accept half on the detach side.
//
// The fixture declares nports=8 and marks port 7 used; port 7 is the
// last valid slot.
func TestDetachPort_AcceptsInRangePort(t *testing.T) {
	t.Parallel()

	var gotWrites []writeCall

	writer := func(path, data string) error {
		gotWrites = append(gotWrites, writeCall{Path: path, Data: data})

		return nil
	}

	mfs := attachFS()

	mfs[testFSVHCIController0StatusPath] = &fstest.MapFile{Data: []byte(
		"hub port sta spd dev      sockfd local_busid\n" +
			"ss  0007 003 005 01020304 000005 2-4\n",
	)}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	err = a.DetachPort(context.Background(), domain.PortID(7))
	require.NoError(t, err,
		"flat port 7 (nports-1=7) is the last valid slot; validator must accept it")
	require.Len(t, gotWrites, 1)
	require.Equal(t, "/sys/devices/platform/vhci_hcd.0/detach", gotWrites[0].Path)
	require.Equal(t, "7", gotWrites[0].Data,
		"detach payload must be the bare decimal of the flat port id")
}

// TestDetachPort_RejectsNonLivePort confirms the adapter validates the
// current status row while holding the port-mutation lock. An in-range
// port that is either explicitly free or absent from the snapshot is
// not a live kernel attachment and must never reach the detach sysfs
// write.
func TestDetachPort_RejectsNonLivePort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		port domain.PortID
	}{
		{name: "free row", port: domain.PortID(0)},
		{name: "absent row", port: domain.PortID(3)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var writes atomic.Int32

			a, err := kernel.NewImporterAdapter(
				kernel.WithFS(attachFS()),
				kernel.WithWriteFunc(func(string, string) error {
					writes.Add(1)

					return nil
				}),
			)
			require.NoError(t, err)

			err = a.DetachPort(t.Context(), tt.port)
			require.ErrorIs(t, err, domain.ErrDeviceNotBound)
			require.EqualValues(t, 0, writes.Load(),
				"a non-live port must be rejected before any sysfs write")
		})
	}
}

func TestDetachPort_PropagatesStatusReadFailure(t *testing.T) {
	t.Parallel()

	var writes atomic.Int32

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(&failSecondStatusOpenFS{inner: attachFS()}),
		kernel.WithWriteFunc(func(string, string) error {
			writes.Add(1)

			return nil
		}),
	)
	require.NoError(t, err)

	err = a.DetachPort(t.Context(), domain.PortID(0))
	require.ErrorIs(t, err, fs.ErrPermission)
	require.EqualValues(t, 0, writes.Load(),
		"a status-read failure must be returned before any sysfs write")
}

// TestDetachPort_SucceedsDespiteIncompleteBusMap pins the
// layering invariant on the detach side: bounds validation must
// consume only StatusTopology (NControllers + VHCIPorts), never the
// BusMap. A controller mid-probe whose usb* children are not yet
// populated in sysfs produces an incomplete BusMap —
// discoverTopology rejects that snapshot with errTopologyIncomplete.
// Routing detach through the full loadTopology would tie every
// detach to BusMap completeness and spuriously fail an otherwise
// in-range detach on a transient shortfall that has nothing to do
// with the bounds arithmetic.
//
// Symmetric with TestAttachRemote_SucceedsDespiteIncompleteBusMap:
// detach uses loadStatusTopology for the same reason, and a fixture
// with valid nports + status but no usb* children must succeed for
// an in-range port.
//
// If detach ever routed through loadTopology, DetachPort would
// surface errTopologyIncomplete whenever loadTopology's BusMap
// completeness check failed (vhci_hcd.0 with zero usb children where
// hubsPerController=2 was expected).
func TestDetachPort_SucceedsDespiteIncompleteBusMap(t *testing.T) {
	t.Parallel()

	var writes atomic.Int32

	writer := func(string, string) error {
		writes.Add(1)

		return nil
	}

	// Minimal fixture: modules present, valid nports + status file,
	// but NO usb* children under vhci_hcd.0. discoverTopology would
	// fail with errTopologyIncomplete; discoverStatusTopology must
	// succeed because it never walks usb* children.
	mfs := fstest.MapFS{
		testFSModuleUSBIPCorePath:       &fstest.MapFile{Mode: fs.ModeDir},
		testFSModuleVHCIHCDPath:         &fstest.MapFile{Mode: fs.ModeDir},
		testFSVHCIController0Dir:        &fstest.MapFile{Mode: fs.ModeDir},
		testFSVHCIController0NPortsPath: &fstest.MapFile{Data: []byte("8\n")},
		testFSVHCIController0StatusPath: &fstest.MapFile{Data: []byte(
			"hub port sta spd dev      sockfd local_busid\n" +
				"hs  0000 003 003 01020304 000005 1-1\n",
		)},
	}

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(mfs),
		kernel.WithWriteFunc(writer),
	)
	require.NoError(t, err)

	err = a.DetachPort(context.Background(), domain.PortID(0))
	require.NoError(t, err,
		"detach bounds validation must not depend on BusMap completeness; "+
			"routing through loadTopology would surface errTopologyIncomplete")
	require.EqualValues(t, 1, writes.Load(),
		"valid in-range port must reach sysfs write exactly once")
}

func TestAttachRemote_HappyPath(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)

	defer func() { _ = right.Close() }() // peer side stays open; adapter closes left.

	wrapped := &closeCountingConn{Conn: left}

	var gotWrites []writeCall

	reservedPort := domain.PortID(^uint32(0))

	writer := func(path, data string) error {
		require.Equal(t, domain.PortID(0), reservedPort,
			"selected port must be reserved before the sysfs write")

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
		ReserveLocalPort: func(id domain.PortID) error {
			reservedPort = id

			return nil
		},
	}

	portID, err := a.AttachRemote(context.Background(), wrapped, spec)
	require.NoError(t, err)
	require.EqualValues(t, 0, portID)

	require.Len(t, gotWrites, 1)
	require.Equal(t, testVHCIAttachPath, gotWrites[0].Path)
	// The fd in the payload is a dup of the conn's fd (different number, same socket).
	// Verify all fields except the fd value; the fd must be a positive integer.
	var portVal, fdVal, devIDVal, speedVal uint32

	n, scanErr := fmt.Sscanf(gotWrites[0].Data, "%d %d %d %d", &portVal, &fdVal, &devIDVal, &speedVal)
	require.NoError(t, scanErr)
	require.Equal(t, 4, n)
	require.EqualValues(t, 0, portVal)
	require.Positive(t, fdVal, "attach payload fd must be positive")
	require.Equal(t, uint32(spec.DevID), devIDVal)
	require.Equal(t, uint32(spec.Speed), speedVal)

	require.EqualValues(t, 1, wrapped.closes.Load(),
		"conn must be closed exactly once after successful sysfs write")
}

func TestAttachRemote_PortReservationFailureAbortsBeforeSysfsWrite(t *testing.T) {
	t.Parallel()

	left, right := socketpairConns(t)

	defer func() {
		_ = right.Close()
		_ = left.Close()
	}()

	wrapped := &closeCountingConn{Conn: left}

	var writes atomic.Int32

	a, err := kernel.NewImporterAdapter(
		kernel.WithFS(attachFS()),
		kernel.WithWriteFunc(func(_, _ string) error {
			writes.Add(1)

			return nil
		}),
	)
	require.NoError(t, err)

	spec := app.RemoteDeviceSpec{
		DevID: domain.DeviceID(1),
		Speed: domain.SpeedHigh,
		ReserveLocalPort: func(_ domain.PortID) error {
			return domain.ErrPermission
		},
	}

	_, err = a.AttachRemote(context.Background(), wrapped, spec)
	require.ErrorIs(t, err, domain.ErrPermission)
	require.Zero(t, writes.Load(), "reservation rejection must precede sysfs mutation")
	require.Zero(t, wrapped.closes.Load(), "caller retains conn ownership before handoff")
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

	mfs[testFSVHCIController0StatusPath] = &fstest.MapFile{Data: []byte(
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
	delete(mfs, testFSModuleVHCIHCDPath)

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
//
// hcPorts is the per-hub port count (VHCI_HC_PORTS from the kernel's
// perspective). The kernel splits the flat port space into a HS block
// at flat 0..hcPorts-1 and a SS block at flat hcPorts..nports-1; the
// fixture must reproduce that split so SS-targeted tests find ss-
// labelled rows in the upper half of the status table.
type singlePortStatus struct {
	mu      sync.Mutex
	nports  int
	hcPorts int
	// busy[i]==true means port i is already in use.
	busy []bool
}

// newSinglePortStatus builds a table of size n where only port 0 is
// free. n must be a positive multiple of two on one controller so the
// HS/SS split is well-defined (hcPorts = n/2); callers that want a
// different controller count must extend the fixture.
func newSinglePortStatus(n int) *singlePortStatus {
	busy := make([]bool, n)
	for i := 1; i < n; i++ {
		busy[i] = true
	}

	// One controller => two hubs (HS + SS) => hcPorts = n / 2.
	return &singlePortStatus{nports: n, hcPorts: n / 2, busy: busy}
}

// statusText returns the current status-file bytes. Hub tokens are
// derived from the flat index: [0, hcPorts) → "hs", [hcPorts, nports)
// → "ss", matching the kernel's status_show_vhci layout.
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

		hub := "hs"
		if i >= s.hcPorts {
			hub = "ss"
		}

		lines = append(lines, fmt.Sprintf("%s  %04d %03d 000 00000000 000000 0-0", hub, i, sta))
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
	if name == testFSVHCIController0StatusPath {
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

// TestFindFreePort_SSMatchesFlatBoundary pins the HS/SS row
// classification contract: the singlePortStatus fixture must classify
// rows by the kernel's flat HS/SS boundary (HS rows at flat
// 0..HCPorts-1, SS rows at flat HCPorts..VHCIPorts-1). Emitting "hs"
// on every row regardless of flat index would make any SS-targeted
// search find zero matches and return ErrNoFreePort even when a free
// SS slot existed in the kernel's actual layout.
//
// With nports=8 and one controller the kernel's HCPorts is 4: HS
// rows occupy flat 0..3 and SS rows occupy flat 4..7. The fixture
// marks only port 4 free (the first SS slot); findFreePort for
// SpeedSuper must return 4.
func TestFindFreePort_SSMatchesFlatBoundary(t *testing.T) {
	t.Parallel()

	const (
		testNPorts     = 8
		testHCPorts    = 4 // nports / (nControllers * hubsPerController) = 8 / (1*2)
		freeSSFlatPort = testHCPorts
	)

	state := newSinglePortStatus(testNPorts)

	// Flip the "only free" slot from port 0 (HS) to port 4 (first SS)
	// so the fixture exercises the SS classifier specifically.
	state.busy[0] = true
	state.busy[freeSSFlatPort] = false

	mfs := &mutableStatusFS{
		inner: fstest.MapFS{
			testFSModuleUSBIPCorePath:           &fstest.MapFile{Mode: fs.ModeDir},
			testFSModuleVHCIHCDPath:             &fstest.MapFile{Mode: fs.ModeDir},
			testFSVHCIController0Dir:            &fstest.MapFile{Mode: fs.ModeDir},
			testFSVHCIController0NPortsPath:     &fstest.MapFile{Data: fmt.Appendf(nil, "%d\n", testNPorts)},
			testFSVHCIController0USB1BusNumPath: &fstest.MapFile{Data: []byte("1\n")},
			testFSVHCIController0USB2BusNumPath: &fstest.MapFile{Data: []byte("2\n")},
		},
		state: state,
	}

	a, err := kernel.NewImporterAdapter(kernel.WithFS(mfs))
	require.NoError(t, err)

	got, err := kernel.FindFreePortForTest(a, domain.SpeedSuper)
	require.NoError(t, err,
		"fixture must emit an ss-labelled row at flat %d; if all rows are hs then SS finds nothing",
		freeSSFlatPort)
	require.GreaterOrEqual(t, int(got), testHCPorts,
		"SS free port must sit in the SS range (>= HCPorts)")
	require.EqualValues(t, freeSSFlatPort, got,
		"the only free SS slot is at flat %d", freeSSFlatPort)
}

// TestAttachRemote_SerializedUnderContention exercises importer-lifecycle and exporter-daemon OpenSpec documents:
// concurrent AttachRemote callers contending for a single free port
// must produce exactly one success and one ErrNoFreePort.
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
			testFSModuleUSBIPCorePath:           &fstest.MapFile{Mode: fs.ModeDir},
			testFSModuleVHCIHCDPath:             &fstest.MapFile{Mode: fs.ModeDir},
			testFSVHCIController0Dir:            &fstest.MapFile{Mode: fs.ModeDir},
			testFSVHCIController0NPortsPath:     &fstest.MapFile{Data: fmt.Appendf(nil, "%d\n", testNPorts)},
			testFSVHCIController0USB1BusNumPath: &fstest.MapFile{Data: []byte("1\n")},
			testFSVHCIController0USB2BusNumPath: &fstest.MapFile{Data: []byte("2\n")},
		},
		state: state,
	}

	writer := func(p, data string) error {
		if p != testVHCIAttachPath {
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
