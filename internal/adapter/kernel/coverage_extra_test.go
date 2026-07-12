// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

const testUeventActionKey = "ACTION"

// errFake is a sentinel injected by fake netlink/socket mocks.
var errFakeReceive = errors.New("fake netlink receive error")

// errOnDriverNameRead is an fs.FS that errors with EACCES when the
// caller opens the configured target path. Used to exercise
// currentDriver's non-ENOENT error branch from unbindCurrentDeviceDriver
// — that branch is normally intercepted by checkAlreadyExported in
// Bind, so direct whitebox invocation is the only path to coverage.
type errOnDriverNameRead struct {
	inner  fs.FS
	target string
}

func (e errOnDriverNameRead) Open(name string) (fs.File, error) {
	if name == e.target {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}

	f, err := e.inner.Open(name)
	if err != nil {
		return nil, fmt.Errorf("errOnDriverNameRead delegate: %w", err)
	}

	return f, nil
}

// TestBusIDInterfaceDirs_ReadErrorReturnsNil pins the defensive
// branch in busIDInterfaceDirs: when the parent
// /sys/bus/usb/devices listing fails (sysfs permission lock-down,
// transient I/O glitch), the helper returns nil so the caller —
// deactivateNetdevs — silently no-ops. Production-only state on
// real Linux; the MapFS-backed test exercises it via a poisoned
// Open.
func TestBusIDInterfaceDirs_ReadErrorReturnsNil(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	mfs := fstest.MapFS{
		"sys/bus/usb/devices/" + busID: {Mode: 0o755},
	}

	a := &ExporterAdapter{commonAdapter: commonAdapter{
		fs: errOnDriverNameRead{
			inner:  mfs,
			target: "sys/bus/usb/devices",
		},
		write:  func(_, _ string) error { return nil },
		logger: slog.Default(),
	}}

	got := a.busIDInterfaceDirs(domain.BusID(busID))
	require.Nil(t, got,
		"busIDInterfaceDirs must return nil on listing error so callers no-op cleanly")
}

// TestUnbindCurrentDeviceDriver_NonAbsenceErrorPropagates pins the
// defensive depth restored after the round-2 refactor:
// unbindCurrentDeviceDriver MUST surface a non-absence currentDriver
// error rather than silently swallowing it. The Bind path's
// checkAlreadyExported normally surfaces such errors first, but
// sysfs state can drift between the two reads (driver hot-detach,
// permission change). Direct whitebox invocation exercises the
// explicit branch.
func TestUnbindCurrentDeviceDriver_NonAbsenceErrorPropagates(t *testing.T) {
	t.Parallel()

	const busID = "1-1"

	mfs := fstest.MapFS{
		"sys/bus/usb/devices/" + busID:                         {Mode: 0o755},
		"sys/bus/usb/devices/" + busID + "/driver":             {Data: []byte("usb\n")},
		"sys/bus/usb/devices/" + busID + "/driver/driver_name": {Data: []byte("usb\n")},
	}

	a := &ExporterAdapter{commonAdapter: commonAdapter{
		fs: errOnDriverNameRead{
			inner:  mfs,
			target: "sys/bus/usb/devices/" + busID + "/driver/driver_name",
		},
		write:  func(_, _ string) error { return nil },
		logger: slog.Default(),
	}}

	err := a.unbindCurrentDeviceDriver(domain.BusID(busID))
	require.Error(t, err,
		"non-absence currentDriver read failure must propagate — defense in depth")
	require.NotErrorIs(t, err, domain.ErrDeviceNotBound,
		"propagated error must NOT be ErrDeviceNotBound — that branch is the absence-shaped no-op")
}

// TestMapUDCEventBranches covers every branch of mapUDCEvent: the
// add/change paths emit DeviceBoundEvent, remove emits
// DeviceUnboundEvent, an unknown action returns ok=false, and a
// devpath that does not match the UDC pattern also returns
// ok=false. The function is unreachable from the public adapter
// surface today (vudc events are wired in via the dispatcher) so
// direct tests are the only path to coverage.
func TestMapUDCEventBranches(t *testing.T) {
	t.Parallel()

	const (
		deviceBoundEventTypeName = "DeviceBoundEvent"
		goodDevpath              = "/devices/platform/dummy_udc/udc/usbip-vudc.0"
	)

	cases := []struct {
		name    string
		action  string
		devpath string
		wantOK  bool
		wantTyp string
	}{
		{"add emits bound", ueventActionAdd, goodDevpath, true, deviceBoundEventTypeName},
		{"change emits bound", "change", goodDevpath, true, deviceBoundEventTypeName},
		{"remove emits unbound", "remove", goodDevpath, true, "DeviceUnboundEvent"},
		{"unknown action rejected", "online", goodDevpath, false, ""},
		{"non-udc devpath rejected", ueventActionAdd, "/devices/platform/foo", false, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ev, ok := mapUDCEvent(tc.action, tc.devpath)
			require.Equal(t, tc.wantOK, ok)

			if !tc.wantOK {
				require.Nil(t, ev)
				return
			}

			require.NotNil(t, ev)

			switch tc.wantTyp {
			case deviceBoundEventTypeName:
				_, ok := ev.(domain.DeviceBoundEvent)
				require.True(t, ok, "expected %s, got %T", deviceBoundEventTypeName, ev)
			case "DeviceUnboundEvent":
				_, ok := ev.(domain.DeviceUnboundEvent)
				require.True(t, ok, "expected DeviceUnboundEvent, got %T", ev)
			}
		})
	}
}

// TestParseStatusRowNumbersErrorBranches covers every error return
// of parseStatusRowNumbers: a non-numeric value in any of the four
// positional columns must surface ok=false and a zero-valued
// statusNums result. Existing tests cover the success path; this
// pins each individual reject branch.
func TestParseStatusRowNumbersErrorBranches(t *testing.T) {
	t.Parallel()

	mkRow := func(port, status, speed, devid string) []string {
		row := []string{
			"hub", port, status, speed, devid,
			"deadbeef", "garbage", "0",
		}

		return row
	}

	cases := []struct {
		name string
		row  []string
	}{
		{"port not a number", mkRow("xx", "0", "5", "1234")},
		{"status not a number", mkRow("0", "xx", "5", "1234")},
		{"speed not a number", mkRow("0", "0", "xx", "1234")},
		{"devid not hex", mkRow("0", "0", "5", "zz")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, ok := parseStatusRowNumbers(tc.row)
			require.False(t, ok)
		})
	}
}

// TestTopologyStatusProjection covers Topology.Status which projects
// the full topology into the operations-observability OpenSpec status JSON shape. A simple value
// projection: the four scalar fields must round-trip.
func TestTopologyStatusProjection(t *testing.T) {
	t.Parallel()

	topo := Topology{
		NControllers: 2,
		HCPorts:      4,
		VHCIPorts:    8,
	}

	got := topo.Status()
	require.Equal(t, uint32(2), got.NControllers)
	require.Equal(t, uint32(4), got.HCPorts)
	require.Equal(t, uint32(8), got.VHCIPorts)
}

// TestWithLoggerNilSelectsDiscardingHandler covers WithLogger's nil-
// argument branch, which installs a discard handler so call sites
// never have to nil-check the logger pointer.
func TestWithLoggerNilSelectsDiscardingHandler(t *testing.T) {
	t.Parallel()

	c := &commonAdapter{}
	WithLogger(nil)(c)
	require.NotNil(t, c.logger,
		"WithLogger(nil) must install a non-nil discarding handler")
}

// TestWithLoggerNonNilStores covers the non-nil branch.
func TestWithLoggerNonNilStores(t *testing.T) {
	t.Parallel()

	c := &commonAdapter{}
	logger := slog.Default()
	WithLogger(logger)(c)
	require.Equal(t, logger, c.logger)
}

// TestWithFSNilGuard covers WithFS's no-op-on-nil branch. The guard
// keeps callers that compose options conditionally from clobbering
// a previously-set FS with nil.
func TestWithFSNilGuard(t *testing.T) {
	t.Parallel()

	c := &commonAdapter{fs: fstest.MapFS{}}
	WithFS(nil)(c)
	require.NotNil(t, c.fs, "WithFS(nil) must not clear an existing fs.FS")
}

// TestWithWriteFuncNilGuard mirrors TestWithFSNilGuard for WithWriteFunc.
func TestWithWriteFuncNilGuard(t *testing.T) {
	t.Parallel()

	prev := WriteFunc(func(_, _ string) error { return nil })
	c := &commonAdapter{write: prev}
	WithWriteFunc(nil)(c)
	require.NotNil(t, c.write,
		"WithWriteFunc(nil) must not clear an existing WriteFunc")
}

// TestWithNetlinkDialerNilGuard mirrors the nil-guard pattern for
// WithNetlinkDialer.
func TestWithNetlinkDialerNilGuard(t *testing.T) {
	t.Parallel()

	prev := NetlinkDialer(func() (NetlinkSocket, error) {
		return nil, errFakeReceive
	})
	c := &commonAdapter{nlDial: prev}
	WithNetlinkDialer(nil)(c)
	require.NotNil(t, c.nlDial,
		"WithNetlinkDialer(nil) must not clear an existing dialer")
}

// TestWithClockNilGuard mirrors the nil-guard pattern for WithClock.
func TestWithClockNilGuard(t *testing.T) {
	t.Parallel()

	c := &commonAdapter{}
	WithClock(nil)(c)
	// commonAdapter zero-value clock is permitted; only assert the
	// option function does not panic on nil and leaves the clock
	// field unmodified relative to the zero value.
	require.Nil(t, c.clock, "WithClock(nil) must not synthesize a clock")
}

// TestNewDispatcherInitialState covers newDispatcher: returned
// dispatcher has a non-nil subscriber map, fresh stop/done channels,
// and a non-nil logger ref.
func TestNewDispatcherInitialState(t *testing.T) {
	t.Parallel()

	d := newDispatcher(nil, slog.Default(), vhciEventMapper{})
	require.NotNil(t, d)
	require.NotNil(t, d.subscribers)
	require.NotNil(t, d.stop)
	require.NotNil(t, d.done)
	require.NotNil(t, d.logger)
}

// TestClassifySyscallErr_DelegatesToClassifyFSErr pins that
// classifySyscallErr delegates to classifyFSErr, which classifies the
// errno into its domain sentinel.
func TestClassifySyscallErr_DelegatesToClassifyFSErr(t *testing.T) {
	t.Parallel()

	const bindPath = "/sys/bus/usb/drivers/usbip-host/bind"

	err := classifySyscallErr("write", bindPath, unix.EIO)
	require.Error(t, err)
	require.Contains(t, err.Error(), "write")
}

// TestClassifyENOENT_KindOther pins the kindOther case: an ENOENT on a
// path that does not fall under devices, drivers, controllers, or
// modules surfaces as a raw "sysfs ENOENT" error rather than a domain
// sentinel, so the caller sees the underlying errno unchanged.
func TestClassifyENOENT_KindOther(t *testing.T) {
	t.Parallel()

	err := classifyENOENT(kindOther, unix.ENOENT)
	require.Error(t, err)
	require.ErrorContains(t, err, "sysfs ENOENT")
	require.NotErrorIs(t, err, domain.ErrDeviceNotFound)
	require.NotErrorIs(t, err, domain.ErrKernelModuleMissing)
}

// TestClassifyENOENT_Default pins the default branch: a pathKind value
// outside the known enum must also produce a "sysfs ENOENT" error (same
// safe fallback as kindOther).
func TestClassifyENOENT_Default(t *testing.T) {
	t.Parallel()

	err := classifyENOENT(pathKind(99), unix.ENOENT)
	require.Error(t, err)
	require.ErrorContains(t, err, "sysfs ENOENT")
}

// TestHandleReceiveErr_NonEOFLogsAndCoversNewDispatcherClosure pins the
// non-EOF branch of handleReceiveErr: an error that is not io.EOF must be
// forwarded to d.logger.warn. The call exercises the warn closure created
// inside newDispatcher, covering that closure body.
func TestHandleReceiveErr_NonEOFLogs(t *testing.T) {
	t.Parallel()

	d := newDispatcher(nil, slog.Default(), vhciEventMapper{})
	// Must not panic; the non-EOF error is logged, not returned.
	d.handleReceiveErr(errFakeReceive)
}

// TestBroadcast_DropsEventOnFullChannel pins the default branch inside
// broadcast: a subscriber whose channel is full (unbuffered, no reader)
// must receive the drop-log path without blocking the broadcaster.
func TestBroadcast_DropsEventOnFullChannel(t *testing.T) {
	t.Parallel()

	d := newDispatcher(nil, slog.Default(), vhciEventMapper{})
	// Unbuffered channel — any send blocks immediately, so the select
	// takes the default branch and logs the drop.
	ch := make(chan domain.Event)
	d.addSubscriber(ch)
	d.broadcast(domain.DeviceBoundEvent{})
}

// TestMapEvent_MissingDEVPATH pins the DEVPATH-absent branch of
// vhciEventMapper.mapEvent: an interesting uevent without a DEVPATH key
// must return (nil, false) so no event is broadcast.
func TestMapEvent_MissingDEVPATH(t *testing.T) {
	t.Parallel()

	m := newVHCIEventMapperWithLoader(func() (Topology, error) { return Topology{}, nil })
	// SUBSYSTEM=usb passes isInterestingUevent; DEVPATH is absent.
	ev, ok := m.mapEvent(map[string]string{"SUBSYSTEM": "usb", testUeventActionKey: "add"})
	require.False(t, ok)
	require.Nil(t, ev)
}

// TestVhciActionToEvent_UnknownAction pins the default branch: an ACTION
// value that is not add/remove/change must return (nil, false) so
// unrecognised kernel actions do not synthesise spurious events.
func TestVhciActionToEvent_UnknownAction(t *testing.T) {
	t.Parallel()

	ev, ok := vhciActionToEvent("online", 0, "")
	require.False(t, ok)
	require.Nil(t, ev)
}

// TestMapUSBDriverEvent_UntrackedUnbind pins the fail-closed branch: an
// unrelated USB unbind without a preceding usbip-host bind must not emit a
// DeviceUnboundEvent.
func TestMapUSBDriverEvent_UntrackedUnbind(t *testing.T) {
	t.Parallel()

	mapper := newVHCIEventMapper(Topology{})
	ev, ok := mapper.mapUSBDriverEvent(map[string]string{
		testUeventActionKey: ueventActionUnbind,
		"SUBSYSTEM":         ueventSubsystemUSB,
	}, "/devices/1-1")
	require.False(t, ok)
	require.Nil(t, ev)
}

// TestMapUSBDriverEvent_OtherDriverBind pins the fail-closed driver filter:
// ordinary USB driver bind notifications must not become usbip-host lifecycle
// events.
func TestMapUSBDriverEvent_OtherDriverBind(t *testing.T) {
	t.Parallel()

	const otherUSBDriver = "usbhid"

	mapper := newVHCIEventMapper(Topology{})
	ev, ok := mapper.mapUSBDriverEvent(map[string]string{
		testUeventActionKey: ueventActionBind,
		"DRIVER":            otherUSBDriver,
	}, "/devices/1-1")
	require.False(t, ok)
	require.Nil(t, ev)
}

// TestRemoveSubscriber_UnknownIDReturnsFalse pins the early-exit branch
// of removeSubscriber: removing an id that was never registered must
// return false and must not panic.
func TestRemoveSubscriber_UnknownIDReturnsFalse(t *testing.T) {
	t.Parallel()

	d := newDispatcher(nil, slog.Default(), vhciEventMapper{})
	got := d.removeSubscriber(9999)
	require.False(t, got, "removeSubscriber must return false for an unregistered id")
}
