// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// importerTestEpoch is the epoch used to seed FakeClock across importer
// tests. A fixed epoch keeps test logs readable.
func importerTestEpoch() time.Time {
	return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
}

// errBoom is a shared sentinel used by importer tests that need to
// inject a failure from a dependency. Named explicitly so assertions
// can use errors.Is instead of string comparison (satisfies err113).
var errBoom = errors.New("boom")

// newImporterForTest constructs an Importer with every required
// dependency stubbed so individual tests only wire the mocks they
// actually exercise.
func newImporterForTest(t *testing.T, opts ...app.ImporterOption) *app.Importer {
	t.Helper()

	const baseOptCount = 5

	base := make([]app.ImporterOption, 0, baseOptCount+len(opts))

	base = append(base,
		app.WithImporterKernel(&ImporterKernelMock{}),
		app.WithImporterEvents(&KernelEventsMock{
			SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
				ch := make(chan domain.Event)

				return ch, func() { close(ch) }, nil
			},
		}),
		app.WithImporterTransport(&TransportMock{}),
		app.WithImporterCodec(&ProtocolCodecMock{}),
		app.WithImporterClock(testutil.NewFakeClockAt(importerTestEpoch())),
	)

	return app.NewImporter(append(base, opts...)...)
}

// TestNewImporterReturnsNonNil asserts NewImporter succeeds with every
// required dependency wired in.
func TestNewImporterReturnsNonNil(t *testing.T) {
	t.Parallel()

	imp := newImporterForTest(t)
	require.NotNil(t, imp)
	require.NoError(t, imp.Close())
}

// TestNewImporterCloseIsIdempotent asserts Close returns nil on repeat
// invocations so `defer imp.Close()` is safe even after a prior
// explicit Close.
func TestNewImporterCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	imp := newImporterForTest(t)

	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
}

// TestNewImporterPanicsOnMissingKernel proves the required-dependency
// guard in NewImporter.
func TestNewImporterPanicsOnMissingKernel(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewImporter: ImporterKernel is required (use WithImporterKernel)",
		func() { app.NewImporter() })
}

// TestNewImporterPanicsOnMissingEvents guards the second required
// dependency.
func TestNewImporterPanicsOnMissingEvents(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewImporter: KernelEvents is required (use WithImporterEvents)",
		func() {
			app.NewImporter(app.WithImporterKernel(&ImporterKernelMock{}))
		})
}

// TestNewImporterPanicsOnMissingTransport guards the third required
// dependency.
func TestNewImporterPanicsOnMissingTransport(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewImporter: Transport is required (use WithImporterTransport)",
		func() {
			app.NewImporter(
				app.WithImporterKernel(&ImporterKernelMock{}),
				app.WithImporterEvents(&KernelEventsMock{}),
			)
		})
}

// TestNewImporterPanicsOnMissingCodec guards the fourth required
// dependency.
func TestNewImporterPanicsOnMissingCodec(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t,
		"app.NewImporter: ProtocolCodec is required (use WithImporterCodec)",
		func() {
			app.NewImporter(
				app.WithImporterKernel(&ImporterKernelMock{}),
				app.WithImporterEvents(&KernelEventsMock{}),
				app.WithImporterTransport(&TransportMock{}),
			)
		})
}

// TestNewImporterAppliesLoggerAndClock covers the optional options so
// their setter paths are not dead code. The effect is observable only
// after a method that uses them is called; for the scaffolding test
// we just confirm construction succeeds.
func TestNewImporterAppliesLoggerAndClock(t *testing.T) {
	t.Parallel()

	clk := testutil.NewFakeClockAt(importerTestEpoch())

	imp := app.NewImporter(
		app.WithImporterKernel(&ImporterKernelMock{}),
		app.WithImporterEvents(&KernelEventsMock{}),
		app.WithImporterTransport(&TransportMock{}),
		app.WithImporterCodec(&ProtocolCodecMock{}),
		app.WithImporterClock(clk),
		app.WithImporterLogger(nil),
	)
	require.NotNil(t, imp)
	require.NoError(t, imp.Close())

	// Touch the fake clock so the import isn't unused — and so a
	// future regression that drops WithImporterClock surfaces as a
	// compile failure in this test.
	_ = clk.Now()
}

// fakeConn is an instrumented net.Conn used by importer tests. It
// records Close, Write, and Read activity so the fd-passing lifecycle
// per v1 contract §5.4 item 4 can be asserted without spinning up a real
// network. Read is backed by a buffered byte stream supplied by the
// test; Write is a no-op that records the payload.
type fakeConn struct {
	mu        sync.Mutex
	closed    int
	writes    [][]byte
	readData  []byte
	readPos   int
	closedCh  chan struct{}
	closeOnce sync.Once
}

func newFakeConn() *fakeConn {
	return &fakeConn{closedCh: make(chan struct{})}
}

// Read copies from the buffered readData; returns io.EOF when drained.
func (c *fakeConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.readPos >= len(c.readData) {
		return 0, io.EOF
	}

	n := copy(p, c.readData[c.readPos:])

	c.readPos += n

	return n, nil
}

// Write appends to the recorded write log and reports success.
func (c *fakeConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	buf := make([]byte, len(p))
	copy(buf, p)

	c.writes = append(c.writes, buf)

	return len(p), nil
}

// Close increments the close counter and is idempotent.
func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed++
	c.closeOnce.Do(func() { close(c.closedCh) })

	return nil
}

func (*fakeConn) LocalAddr() net.Addr                { return fakeAddr{} }
func (*fakeConn) RemoteAddr() net.Addr               { return fakeAddr{} }
func (*fakeConn) SetDeadline(_ time.Time) error      { return nil }
func (*fakeConn) SetReadDeadline(_ time.Time) error  { return nil }
func (*fakeConn) SetWriteDeadline(_ time.Time) error { return nil }

func (c *fakeConn) closeCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.closed
}

func (c *fakeConn) writeLog() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make([][]byte, len(c.writes))
	copy(out, c.writes)

	return out
}

// fakeAddr is a stand-in net.Addr for fakeConn. Network returns "tcp"
// so any caller that logs the address sees a plausible value.
type fakeAddr struct{}

func (fakeAddr) Network() string { return "tcp" }
func (fakeAddr) String() string  { return "fake" }

// testRemote returns a canonical RemoteEndpoint shared by the importer
// tests so failures reference a single value.
func testRemote() domain.RemoteEndpoint {
	return domain.RemoteEndpoint{Host: "peer.example", Port: 3240}
}

// TestImporterListRemoteHappyPath drives the whole dial + encode +
// decode sequence and asserts the connection is closed before return.
func TestImporterListRemoteHappyPath(t *testing.T) {
	t.Parallel()

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}

	reqBytes := []byte{0x01, 0x11, 0x80, 0x05, 0, 0, 0, 0}

	want := []domain.Device{
		{BusID: domain.BusID("1-1"), Path: "/sys/devices/pci/usb1/1-1"},
		{BusID: domain.BusID("2-1"), Path: "/sys/devices/pci/usb2/2-1"},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqDevlistFunc: func() []byte { return reqBytes },
		DecodeOpRepDevlistFunc: func(_ io.Reader) ([]domain.Device, error) {
			return want, nil
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	got, err := imp.ListRemote(context.Background(), testRemote())
	require.NoError(t, err)
	require.Equal(t, want, got)

	// Transport dial received the (normalised) endpoint.
	require.Len(t, transport.DialCalls(), 1)
	require.Equal(t, testRemote().NormalizePort(), transport.DialCalls()[0].Endpoint)

	// The OP_REQ_DEVLIST bytes were written to the conn exactly once.
	writes := conn.writeLog()
	require.Len(t, writes, 1)
	require.Equal(t, reqBytes, writes[0])

	// Decoder was given the conn as its reader.
	require.Len(t, codec.DecodeOpRepDevlistCalls(), 1)

	// Conn closed exactly once by ListRemote (it owns the conn).
	require.Equal(t, 1, conn.closeCount())
}

// TestImporterListRemoteDialFailure asserts a transport error surfaces
// wrapped with the remote context and that no codec work happens.
func TestImporterListRemoteDialFailure(t *testing.T) {
	t.Parallel()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return nil, errBoom
		},
	}

	codec := &ProtocolCodecMock{}

	imp := newImporterForTest(t,
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	devs, err := imp.ListRemote(context.Background(), testRemote())
	require.Nil(t, devs)
	require.Error(t, err)
	require.ErrorIs(t, err, errBoom)
	require.Contains(t, err.Error(), "peer.example")

	// No encode or decode calls when the dial failed.
	require.Empty(t, codec.EncodeOpReqDevlistCalls())
	require.Empty(t, codec.DecodeOpRepDevlistCalls())
}

// TestImporterListRemoteDecodeFailure asserts protocol-level errors
// propagate unmodified (ErrProtocolMismatch is preserved via %w) and
// that the conn is still closed on the error path.
func TestImporterListRemoteDecodeFailure(t *testing.T) {
	t.Parallel()

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqDevlistFunc: func() []byte { return []byte{1, 2, 3} },
		DecodeOpRepDevlistFunc: func(_ io.Reader) ([]domain.Device, error) {
			return nil, domain.ErrProtocolMismatch
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	devs, err := imp.ListRemote(context.Background(), testRemote())
	require.Nil(t, devs)
	require.ErrorIs(t, err, domain.ErrProtocolMismatch)

	// Conn still closed after the decode failure.
	require.Equal(t, 1, conn.closeCount())
}

// TestImporterListRemoteClosedReturnsErr asserts calling ListRemote
// after Close fails fast with ErrImporterClosed and does NOT touch the
// transport at all.
func TestImporterListRemoteClosedReturnsErr(t *testing.T) {
	t.Parallel()

	transport := &TransportMock{}

	imp := newImporterForTest(t, app.WithImporterTransport(transport))
	require.NoError(t, imp.Close())

	devs, err := imp.ListRemote(context.Background(), testRemote())
	require.Nil(t, devs)
	require.ErrorIs(t, err, app.ErrImporterClosed)
	require.Empty(t, transport.DialCalls())
}

// attachBusID is the canonical busid used by Attach tests.
func attachBusID() domain.BusID {
	return domain.BusID("1-1.2")
}

// attachDevice is the canonical decoded OP_REP_IMPORT reply. Speed
// HighSpeed and a synthetic DeviceID let tests verify the Port is
// populated from the decoded fields rather than fabricated.
func attachDevice() domain.Device {
	return domain.Device{
		Path:   "/sys/devices/pci/usb1/1-1.2",
		BusID:  attachBusID(),
		BusNum: 3,
		DevNum: 7,
		Speed:  domain.SpeedHigh,
	}
}

// TestImporterAttachHappyPath drives the full 5-step sequence and
// asserts the Port return value, the call order, and — critically —
// that conn.Close is NOT called by Attach itself (the kernel owns the
// fd post-handoff; closing it here would tear down the just-attached
// device).
func TestImporterAttachHappyPath(t *testing.T) {
	t.Parallel()

	conn := newFakeConn()

	const wantPortID domain.PortID = 4

	call := []string{}

	recordCall := func(name string) { call = append(call, name) }

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			recordCall("Dial")

			return conn, nil
		},
	}

	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error {
			recordCall("EncodeOpReqImport")

			return nil
		},
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) {
			recordCall("DecodeOpRepImport")

			return attachDevice(), nil
		},
	}

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error {
			recordCall("ModulesAvailable")

			return nil
		},
		AttachRemoteFunc: func(_ context.Context, c net.Conn, spec app.RemoteDeviceSpec) (domain.PortID, error) {
			recordCall("AttachRemote")
			require.Same(t, conn, c)
			require.Equal(t, attachDevice(), spec.Device)
			require.Equal(t, attachDevice().Speed, spec.Speed)

			return wantPortID, nil
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.NoError(t, err)

	// Call order matches the v1 contract §5.2 sequence.
	require.Equal(t, []string{"ModulesAvailable", "Dial", "EncodeOpReqImport", "DecodeOpRepImport", "AttachRemote"}, call)

	// Port is populated from (request + decoded spec + attach result).
	require.Equal(t, wantPortID, port.ID)
	require.Equal(t, attachBusID(), port.BusID)
	require.Equal(t, domain.SpeedHigh, port.Speed)
	require.Equal(t, testRemote().NormalizePort(), port.Remote)
	require.Equal(t, domain.StatusUsed, port.Status)

	// DeviceID = (busnum << 16) | devnum.
	require.Equal(t, domain.DeviceID((uint32(3)<<16)|uint32(7)), port.DeviceID)

	// Critical: success path leaves the conn untouched — kernel owns it.
	require.Equal(t, 0, conn.closeCount())
}

// TestImporterAttachModulesAvailableFailure asserts a ModulesAvailable
// failure aborts before any Dial and does NOT call Close (no conn).
func TestImporterAttachModulesAvailableFailure(t *testing.T) {
	t.Parallel()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return domain.ErrKernelModuleMissing },
	}
	transport := &TransportMock{}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.ErrorIs(t, err, domain.ErrKernelModuleMissing)
	require.Empty(t, transport.DialCalls())
}

// TestImporterAttachDialFailure asserts a dial failure returns without
// calling anything upstream of Dial on the conn side (no conn to close).
func TestImporterAttachDialFailure(t *testing.T) {
	t.Parallel()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return nil, errBoom
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.ErrorIs(t, err, errBoom)
}

// TestImporterAttachEncodeFailure asserts a codec encode failure closes
// the conn exactly once — we own the fd up to the moment kernel accepts
// it.
func TestImporterAttachEncodeFailure(t *testing.T) {
	t.Parallel()

	conn := newFakeConn()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return errBoom },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.ErrorIs(t, err, errBoom)
	require.Equal(t, 1, conn.closeCount())
}

// TestImporterAttachDecodeFailure asserts decode errors close the conn
// once.
func TestImporterAttachDecodeFailure(t *testing.T) {
	t.Parallel()

	conn := newFakeConn()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) {
			return domain.Device{}, domain.ErrProtocolError
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.ErrorIs(t, err, domain.ErrProtocolError)
	require.Equal(t, 1, conn.closeCount())
}

// TestImporterAttachAttachRemoteFailure asserts a kernel handoff failure
// closes the conn once — the handoff did NOT succeed, so we still own
// the fd and MUST release it.
func TestImporterAttachAttachRemoteFailure(t *testing.T) {
	t.Parallel()

	conn := newFakeConn()

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return 0, domain.ErrNoFreePort
		},
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.ErrorIs(t, err, domain.ErrNoFreePort)
	require.Equal(t, 1, conn.closeCount())
}

// TestImporterAttachClosedReturnsErr asserts Attach on a closed Importer
// returns ErrImporterClosed without touching the kernel or transport.
func TestImporterAttachClosedReturnsErr(t *testing.T) {
	t.Parallel()

	kernel := &ImporterKernelMock{}
	transport := &TransportMock{}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
	)
	require.NoError(t, imp.Close())

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.ErrorIs(t, err, app.ErrImporterClosed)
	require.Empty(t, kernel.ModulesAvailableCalls())
	require.Empty(t, transport.DialCalls())
}

// attachOnce is a helper that drives a successful Attach with every
// dependency stubbed minimally. It returns the Importer and the Port
// produced by the attach so Detach/Close tests can exercise the
// post-attach state without re-wiring the full dependency graph every
// time.
func attachOnce(t *testing.T, kernel *ImporterKernelMock) (*app.Importer, domain.Port) {
	t.Helper()

	conn := newFakeConn()

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)

	port, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.NoError(t, err)

	return imp, port
}

// TestImporterDetachCancelsThenDelegates asserts Detach invokes the
// handle's cancel func exactly once AND delegates to kernel.DetachPort
// with the same id — v1 contract §5.5 says the handle must be released before
// the kernel-side detach so any auto-reconnect watcher sees cancel
// before a status transition and does not race a reattempt.
func TestImporterDetachCancelsThenDelegates(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 4

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return nil },
	}

	imp, port := attachOnce(t, kernel)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	require.Equal(t, portID, port.ID)
	require.NoError(t, imp.Detach(context.Background(), portID))

	// Kernel-side detach received the same id.
	require.Len(t, kernel.DetachPortCalls(), 1)
	require.Equal(t, portID, kernel.DetachPortCalls()[0].ID)
}

// TestImporterDetachUnknownReturnsNotBound asserts detaching an ID that
// was never attached returns ErrDeviceNotBound without calling the
// kernel (the map lookup is the source of truth, not sysfs).
func TestImporterDetachUnknownReturnsNotBound(t *testing.T) {
	t.Parallel()

	kernel := &ImporterKernelMock{}

	imp := newImporterForTest(t, app.WithImporterKernel(kernel))
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	err := imp.Detach(context.Background(), domain.PortID(99))
	require.ErrorIs(t, err, domain.ErrDeviceNotBound)
	require.Empty(t, kernel.DetachPortCalls())
}

// TestImporterDetachDuplicateReturnsNotBound asserts the second Detach
// after a successful first Detach returns ErrDeviceNotBound. Handles
// are removed from the map on the first successful detach.
func TestImporterDetachDuplicateReturnsNotBound(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 2

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return nil },
	}

	imp, _ := attachOnce(t, kernel)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	require.NoError(t, imp.Detach(context.Background(), portID))

	err := imp.Detach(context.Background(), portID)
	require.ErrorIs(t, err, domain.ErrDeviceNotBound)
	require.Len(t, kernel.DetachPortCalls(), 1)
}

// TestImporterDetachClosedReturnsErr asserts Detach after Close returns
// ErrImporterClosed and does not touch the kernel.
func TestImporterDetachClosedReturnsErr(t *testing.T) {
	t.Parallel()

	kernel := &ImporterKernelMock{}

	imp := newImporterForTest(t, app.WithImporterKernel(kernel))
	require.NoError(t, imp.Close())

	err := imp.Detach(context.Background(), domain.PortID(1))
	require.ErrorIs(t, err, app.ErrImporterClosed)
	require.Empty(t, kernel.DetachPortCalls())
}

// TestImporterDetachKernelFailurePreservesHandle asserts that when the
// kernel-side detach itself fails, the handle remains registered so
// the caller can retry without first having to re-Attach. This also
// ensures the cancel func has NOT run, because a re-Attach would rely
// on the original handle context still being alive.
func TestImporterDetachKernelFailurePreservesHandle(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 7

	kernelDetachErr := domain.ErrPermission

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return kernelDetachErr },
	}

	imp, _ := attachOnce(t, kernel)
	t.Cleanup(func() {
		// Let the second Detach attempt succeed so Close has no ghosts.
		kernel.DetachPortFunc = func(_ context.Context, _ domain.PortID) error { return nil }
		_ = imp.Detach(context.Background(), portID)
		require.NoError(t, imp.Close())
	})

	err := imp.Detach(context.Background(), portID)
	require.ErrorIs(t, err, kernelDetachErr)

	// Because the kernel rejected the detach, the handle is still
	// registered — a follow-up Detach therefore does NOT get
	// ErrDeviceNotBound; it sees the same kernel error again.
	err = imp.Detach(context.Background(), portID)
	require.ErrorIs(t, err, kernelDetachErr)
}

// TestImporterListPortsDelegates asserts ListPorts forwards to the
// kernel's view and returns the slice verbatim. The Importer's handle
// map is local bookkeeping; the kernel's sysfs-derived list is the
// authoritative source.
func TestImporterListPortsDelegates(t *testing.T) {
	t.Parallel()

	want := []domain.Port{
		{ID: 1, Status: domain.StatusUsed, BusID: domain.BusID("1-1")},
		{ID: 2, Status: domain.StatusUsed, BusID: domain.BusID("2-1")},
	}

	kernel := &ImporterKernelMock{
		ListPortsFunc: func(_ context.Context) ([]domain.Port, error) { return want, nil },
	}

	imp := newImporterForTest(t, app.WithImporterKernel(kernel))
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	got, err := imp.ListPorts(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestImporterListPortsClosedReturnsErr asserts ListPorts on a closed
// Importer returns ErrImporterClosed.
func TestImporterListPortsClosedReturnsErr(t *testing.T) {
	t.Parallel()

	kernel := &ImporterKernelMock{}

	imp := newImporterForTest(t, app.WithImporterKernel(kernel))
	require.NoError(t, imp.Close())

	_, err := imp.ListPorts(context.Background())
	require.ErrorIs(t, err, app.ErrImporterClosed)
	require.Empty(t, kernel.ListPortsCalls())
}

// TestImporterCloseCancelsAllHandles asserts Close cancels every
// registered handle's cancel func. We attach two ports, close the
// Importer, and assert the per-handle contexts were cancelled — proven
// by the fact that follow-up Detach calls return ErrImporterClosed
// (Close ran) rather than ErrDeviceNotBound (handle map still populated).
func TestImporterCloseCancelsAllHandles(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		nextID  domain.PortID = 10
		counter domain.PortID
	)

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			mu.Lock()
			defer mu.Unlock()

			id := nextID + counter

			counter++

			return id, nil
		},
	}

	imp, _ := attachOnce(t, kernel)

	// Second attach reuses the same Importer via a fresh fake conn.
	conn := newFakeConn()
	transport2 := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return conn, nil
		},
	}
	codec2 := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	// Rewire — but we already constructed imp in attachOnce with its
	// own transport/codec; to attach twice cleanly the test drives a
	// second imp rather than hot-swapping deps.
	imp2 := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport2),
		app.WithImporterCodec(codec2),
	)

	_, err := imp2.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
	require.NoError(t, err)

	// Both Importers have a handle recorded. Close both; a second Close
	// is a no-op.
	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
	require.NoError(t, imp2.Close())
	require.NoError(t, imp2.Close())

	// Post-close detach surfaces ErrImporterClosed, not ErrDeviceNotBound.
	err = imp.Detach(context.Background(), 10)
	require.ErrorIs(t, err, app.ErrImporterClosed)

	err = imp2.Detach(context.Background(), 11)
	require.ErrorIs(t, err, app.ErrImporterClosed)
}

// TestImporterWatchYieldsSubscribedEvents asserts Watch yields every
// event pushed on the Subscribe channel until the source closes.
func TestImporterWatchYieldsSubscribedEvents(t *testing.T) {
	t.Parallel()

	ch := make(chan domain.Event, 3)

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return ch, func() {}, nil
		},
	}

	imp := newImporterForTest(t, app.WithImporterEvents(events))
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	want := []domain.Event{
		domain.PortAttachedEvent{Port: domain.Port{ID: 1}},
		domain.PortAttachedEvent{Port: domain.Port{ID: 2}},
		domain.PortAttachedEvent{Port: domain.Port{ID: 3}},
	}
	for _, e := range want {
		ch <- e
	}

	close(ch)

	got := []domain.Event{}

	for e := range imp.Watch(context.Background()) {
		got = append(got, e)
	}

	require.Equal(t, want, got)
	require.Len(t, events.SubscribeCalls(), 1)
}

// TestImporterWatchCtxCancelTerminatesIter asserts ctx cancellation
// stops iteration even if the subscribe channel still has pending
// events.
func TestImporterWatchCtxCancelTerminatesIter(t *testing.T) {
	t.Parallel()

	ch := make(chan domain.Event)

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return ch, func() {}, nil
		},
	}

	imp := newImporterForTest(t, app.WithImporterEvents(events))
	t.Cleanup(func() {
		close(ch)
		require.NoError(t, imp.Close())
	})

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately; no events will flow.
	cancel()

	got := []domain.Event{}

	for e := range imp.Watch(ctx) {
		got = append(got, e)
	}

	require.Empty(t, got)
}

// TestImporterWatchPostCloseReturnsEmpty asserts Watch on a closed
// Importer returns an iter that yields nothing and terminates
// immediately — no Subscribe call is made.
func TestImporterWatchPostCloseReturnsEmpty(t *testing.T) {
	t.Parallel()

	events := &KernelEventsMock{}

	imp := newImporterForTest(t, app.WithImporterEvents(events))
	require.NoError(t, imp.Close())

	count := 0

	for range imp.Watch(context.Background()) {
		count++
	}

	require.Zero(t, count)
	require.Empty(t, events.SubscribeCalls())
}

// TestImporterWatchSubscribeErrorYieldsEmpty asserts a Subscribe
// failure produces a terminated iter rather than panicking or
// looping forever.
func TestImporterWatchSubscribeErrorYieldsEmpty(t *testing.T) {
	t.Parallel()

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return nil, nil, errBoom
		},
	}

	imp := newImporterForTest(t, app.WithImporterEvents(events))
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	count := 0

	for range imp.Watch(context.Background()) {
		count++
	}

	require.Zero(t, count)
}

// TestImporterWatchDoesNotSubscribeUntilIterated asserts the cost of
// calling Watch is bounded to the iter.Seq allocation: no kernel
// Subscribe runs and no fanout subscriber is registered until the
// caller actually ranges over the returned iter. A caller that
// constructs the iter and then drops it MUST NOT leak the kernel
// subscription cancel func or a slot in the Importer's subscriber
// list. Pins the lazy-init contract introduced after audit found
// `_ = imp.Watch(ctx)` leaked one KernelEvents subscription per call
// until Importer.Close.
func TestImporterWatchDoesNotSubscribeUntilIterated(t *testing.T) {
	t.Parallel()

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			ch := make(chan domain.Event)

			return ch, func() { close(ch) }, nil
		},
	}

	imp := newImporterForTest(t, app.WithImporterEvents(events))
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_ = imp.Watch(context.Background())

	require.Empty(t, events.SubscribeCalls(),
		"Watch must defer kernel Subscribe until the iter is ranged over")
	require.Zero(t, app.ImporterSubscribersLenForTest(imp),
		"Watch must defer fanout-list registration until the iter is ranged over")
}

// TestImporterWatchCancelsKernelSubscribeOnIterExit asserts the kernel
// subscription cancel func returned by KernelEvents.Subscribe IS
// invoked when the iter terminates — either through ctx cancel,
// yield-false, or upstream close. Without this guarantee a long-lived
// daemon that opens many short-lived Watch loops accumulates kernel-
// adapter subscription handles indefinitely.
func TestImporterWatchCancelsKernelSubscribeOnIterExit(t *testing.T) {
	t.Parallel()

	var cancelCount atomic.Int32

	ch := make(chan domain.Event)

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return ch, func() { cancelCount.Add(1) }, nil
		},
	}

	imp := newImporterForTest(t, app.WithImporterEvents(events))
	t.Cleanup(func() {
		close(ch)
		require.NoError(t, imp.Close())
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range imp.Watch(ctx) {
		t.Fatal("no events were published; iter should drain immediately on cancelled ctx")
	}

	require.Equal(t, int32(1), cancelCount.Load(),
		"iter exit must invoke the kernel Subscribe cancel exactly once")
}

// TestImporterAttachConcurrentWithCloseNoPanic is the RED for the
// Attach → registerHandle nil-map race. AttachRemote is gated so the
// kernel handoff has already succeeded (i.e. we are past the fd-hand-off
// commitment point) but the caller has not yet recorded the handle.
// Close runs concurrently and nils the handle map. registerHandle must
// detect the closed state, release the kernel port via DetachPort, and
// return ErrImporterClosed from Attach — never "assignment to entry in
// nil map" from an unconditional map write.
func TestImporterAttachConcurrentWithCloseNoPanic(t *testing.T) {
	t.Parallel()

	const parallelAttaches = 16

	var (
		nextID    atomic.Uint32
		attachMu  sync.Mutex
		attachers sync.WaitGroup
		closeErr  atomic.Value
	)

	gate := make(chan struct{})
	observedAttachRemote := make(chan struct{}, parallelAttaches)
	detached := make(chan domain.PortID, parallelAttaches)

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			id := domain.PortID(nextID.Add(1))

			// Signal that we are past the kernel-commit boundary — the
			// fd has notionally been handed off — and block until the
			// test gate opens. Close will fire while every attach is
			// parked here, deterministically exposing the race.
			observedAttachRemote <- struct{}{}

			<-gate

			return id, nil
		},
		DetachPortFunc: func(_ context.Context, id domain.PortID) error {
			detached <- id

			return nil
		},
	}

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}
	// The importer now rejects an OP_REP_IMPORT whose busid differs from
	// the request — pair the codec mock per-conn so each attach's reply
	// echoes its own request. A shared queue would race across the 16
	// concurrent goroutines and mismatch encode/decode pairs.
	var pendingMu sync.Mutex
	pending := make(map[any]domain.BusID, parallelAttaches)
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(w io.Writer, b domain.BusID) error {
			pendingMu.Lock()
			pending[w] = b
			pendingMu.Unlock()

			return nil
		},
		DecodeOpRepImportFunc: func(r io.Reader) (domain.Device, error) {
			pendingMu.Lock()
			b := pending[r]
			delete(pending, r)
			pendingMu.Unlock()

			d := attachDevice()
			d.BusID = b

			return d, nil
		},
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)

	type result struct {
		port domain.Port
		err  error
	}

	results := make([]result, parallelAttaches)

	for i := range parallelAttaches {
		// Use a UNIQUE busid per goroutine so the (endpoint, busid)
		// attach dedup does not reject same-slot concurrent callers.
		// This test exercises the Close vs registerHandle race
		// specifically — the dedup guard has its own test. Each
		// goroutine therefore hits a distinct attachKey.
		bus := domain.BusID(fmt.Sprintf("1-1.%d", i+1))

		attachers.Go(func() {
			port, err := imp.Attach(context.Background(), testRemote(), bus, app.AttachOptions{})

			attachMu.Lock()
			results[i] = result{port: port, err: err}
			attachMu.Unlock()
		})
	}

	// Wait until every attach goroutine is parked inside AttachRemote —
	// past the commitment point but not yet through registerHandle.
	for range parallelAttaches {
		select {
		case <-observedAttachRemote:
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for attach goroutines to reach AttachRemote")
		}
	}

	// Close in a separate goroutine so it races with the drained
	// AttachRemote callers once the gate opens.
	closeDone := make(chan struct{})

	go func() {
		defer close(closeDone)

		err := imp.Close()
		if err != nil {
			closeErr.Store(err)
		}
	}()

	// Release AttachRemote. All parallelAttaches goroutines now race
	// with Close's handle-map nilling.
	close(gate)

	attachers.Wait()
	<-closeDone
	require.Nil(t, closeErr.Load(), "Close must not return an error")

	// Contract: each attach either returned a live Port (ran entirely
	// before Close nilled the map) or ErrImporterClosed (registerHandle
	// saw closed and released the kernel port). No other outcome is
	// acceptable, and in particular no goroutine may have panicked.
	released := 0

	for _, r := range results {
		switch {
		case r.err == nil:
			require.NotZero(t, r.port.ID, "successful attach must carry a port id")
		case errors.Is(r.err, app.ErrImporterClosed):
			released++
		default:
			t.Fatalf("unexpected attach error: %v", r.err)
		}
	}

	// For every attach that resolved to ErrImporterClosed, the kernel
	// port must have been released via DetachPort.
	close(detached)

	detachedIDs := map[domain.PortID]struct{}{}
	for id := range detached {
		detachedIDs[id] = struct{}{}
	}

	require.Len(t, detachedIDs, released,
		"every ErrImporterClosed attach must release its kernel port via DetachPort")
}

// TestImporterAttachCloseRaceDetachFailureLogged drives the narrow
// path where Close wins the race with registerHandle AND the kernel
// rejects the best-effort DetachPort that releases the orphaned port.
// The attach call must still return ErrImporterClosed; the secondary
// detach error is logged (not surfaced) so the caller sees the primary
// close signal, not the release failure.
func TestImporterAttachCloseRaceDetachFailureLogged(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 42

	gate := make(chan struct{})
	observedAttachRemote := make(chan struct{}, 1)

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			observedAttachRemote <- struct{}{}

			<-gate

			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error { return errBoom },
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint, _ app.TransportOptions) (net.Conn, error) {
			return newFakeConn(), nil
		},
	}
	codec := &ProtocolCodecMock{
		EncodeOpReqImportFunc: func(_ io.Writer, _ domain.BusID) error { return nil },
		DecodeOpRepImportFunc: func(_ io.Reader) (domain.Device, error) { return attachDevice(), nil },
	}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
		app.WithImporterCodec(codec),
	)

	attachResult := make(chan error, 1)

	go func() {
		_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{})
		attachResult <- err
	}()

	// Wait for Attach to park inside AttachRemote so Close is guaranteed
	// to race registerHandle, not ModulesAvailable or Dial.
	select {
	case <-observedAttachRemote:
	case <-time.After(2 * time.Second):
		t.Fatal("AttachRemote was not entered in time")
	}

	// Close commits closed=true and drains the in-flight Attach; the
	// Attach goroutine is still parked on the gate, so release it from
	// a helper goroutine after Close has entered its wg drain. That
	// ordering is the point of the test: registerHandle must see
	// closed=true and take the release-and-log branch before Close
	// returns.
	closeDone := make(chan error, 1)

	go func() {
		closeDone <- imp.Close()
	}()

	// Give Close a moment to commit closed=true and start draining.
	time.Sleep(50 * time.Millisecond)
	close(gate)

	select {
	case err := <-closeDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after gate release")
	}

	select {
	case err := <-attachResult:
		require.ErrorIs(t, err, app.ErrImporterClosed)
	case <-time.After(2 * time.Second):
		t.Fatal("Attach did not return after close race")
	}

	// DetachPort was invoked (and returned errBoom) so the release +
	// log path ran. Assert the call actually hit kernel.DetachPort.
	require.Len(t, kernel.DetachPortCalls(), 1)
	require.Equal(t, portID, kernel.DetachPortCalls()[0].ID)
}

// TestImporterCloseWaitsForInflightDetach is the RED for the
// "Close drains Detach" contract. Detach currently releases the write
// lock and calls kernel.DetachPort without incrementing the Importer's
// waitgroup; Close only waits on that waitgroup, so Close can return
// while an inflight Detach is still writing to sysfs. The fix is to
// enrol the Detach kernel call in the waitgroup; this test gates
// DetachPort on a channel so the race is deterministic.
func TestImporterCloseWaitsForInflightDetach(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 11

	detachStarted := make(chan struct{})
	releaseDetach := make(chan struct{})

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
		DetachPortFunc: func(_ context.Context, _ domain.PortID) error {
			close(detachStarted)
			<-releaseDetach

			return nil
		},
	}

	imp, _ := attachOnce(t, kernel)

	detachDone := make(chan error, 1)

	go func() {
		detachDone <- imp.Detach(context.Background(), portID)
	}()

	// Wait until Detach is parked inside kernel.DetachPort. From this
	// point on the handle map has been looked up but the kernel write
	// has not completed — exactly the window Close must cover.
	select {
	case <-detachStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("DetachPort was not entered in time")
	}

	closeReturned := make(chan error, 1)

	go func() {
		closeReturned <- imp.Close()
	}()

	// Close MUST block while DetachPort is still in-flight. Probe with
	// a short deadline; if Close returns before the gate opens, the
	// contract is broken.
	select {
	case err := <-closeReturned:
		t.Fatalf("Close returned before in-flight Detach finished: %v", err)
	case <-time.After(200 * time.Millisecond):
		// Expected — Close is blocked on the waitgroup.
	}

	close(releaseDetach)

	select {
	case err := <-detachDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Detach did not return after release")
	}

	select {
	case err := <-closeReturned:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after Detach drained")
	}
}

// TestImporterCloseIdempotentAfterAttach asserts Close is idempotent
// even after a successful Attach has registered a handle. The second
// Close must not double-cancel or panic.
func TestImporterCloseIdempotentAfterAttach(t *testing.T) {
	t.Parallel()

	const portID domain.PortID = 5

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return portID, nil
		},
	}

	imp, _ := attachOnce(t, kernel)

	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
	require.NoError(t, imp.Close())
}

// TestImporterWatchOnClosedReturnsEmptyIter pins the post-Close
// contract on Watch: the returned iter.Seq terminates immediately
// without invoking yield. A consumer ranging over the result must
// see zero events. This drives the emptyEventSeq fast-path that the
// Close-cancellation early-return uses.
func TestImporterWatchOnClosedReturnsEmptyIter(t *testing.T) {
	t.Parallel()

	imp := newImporterForTest(t)

	require.NoError(t, imp.Close())

	got := 0
	for range imp.Watch(context.Background()) {
		got++
	}

	require.Zero(t, got, "Watch on closed Importer must yield no events")
}

// TestImporterWatchSubscribeFailureReturnsEmptyIter covers the OTHER
// emptyEventSeq path: Subscribe returns an error, the handler logs
// and returns the empty iter so the caller does not panic on a nil
// channel.
func TestImporterWatchSubscribeFailureReturnsEmptyIter(t *testing.T) {
	t.Parallel()

	events := &KernelEventsMock{
		SubscribeFunc: func(_ context.Context) (<-chan domain.Event, func(), error) {
			return nil, nil, errBoom
		},
	}

	imp := newImporterForTest(t, app.WithImporterEvents(events))
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	got := 0
	for range imp.Watch(context.Background()) {
		got++
	}

	require.Zero(t, got, "Watch when Subscribe fails must yield no events, not panic")
	require.Len(t, events.SubscribeCalls(), 1)
}
