package app_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
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
// per spec §5.4 item 4 can be asserted without spinning up a real
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

func newFakeConn(readData []byte) *fakeConn {
	return &fakeConn{readData: readData, closedCh: make(chan struct{})}
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

	conn := newFakeConn(nil)

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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

	conn := newFakeConn(nil)

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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

	conn := newFakeConn(nil)

	const wantPortID domain.PortID = 4

	call := []string{}

	recordCall := func(name string) { call = append(call, name) }

	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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

	// Call order matches the spec §5.2 sequence.
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

// TestImporterAttachAutoReconnectStubbed asserts that AutoReconnect=true
// is rejected with ErrAutoReconnectNotImplemented and no work happens
// (no modules probe, no dial).
func TestImporterAttachAutoReconnectStubbed(t *testing.T) {
	t.Parallel()

	kernel := &ImporterKernelMock{}
	transport := &TransportMock{}

	imp := newImporterForTest(t,
		app.WithImporterKernel(kernel),
		app.WithImporterTransport(transport),
	)
	t.Cleanup(func() { require.NoError(t, imp.Close()) })

	_, err := imp.Attach(context.Background(), testRemote(), attachBusID(), app.AttachOptions{AutoReconnect: true})
	require.ErrorIs(t, err, app.ErrAutoReconnectNotImplemented)

	require.Empty(t, kernel.ModulesAvailableCalls())
	require.Empty(t, transport.DialCalls())
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
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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

	conn := newFakeConn(nil)

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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

	conn := newFakeConn(nil)

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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

	conn := newFakeConn(nil)

	kernel := &ImporterKernelMock{
		ModulesAvailableFunc: func(_ context.Context) error { return nil },
		AttachRemoteFunc: func(_ context.Context, _ net.Conn, _ app.RemoteDeviceSpec) (domain.PortID, error) {
			return 0, domain.ErrNoFreePort
		},
	}
	transport := &TransportMock{
		DialFunc: func(_ context.Context, _ domain.RemoteEndpoint) (net.Conn, error) {
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
