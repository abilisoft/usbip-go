package usbip_test

import (
	"context"
	"io"
	"net"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	internalapp "github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// stubImporterKernel is a hand-rolled stand-in for app.ImporterKernel
// covering exactly the methods pkg/usbip forwarding tests need. Fields
// are function-typed so each test wires the exact behaviour it asserts;
// unset fields return a zero value / nil error by default — any test
// that requires a non-default response must set the relevant field
// explicitly.
type stubImporterKernel struct {
	attachRemoteFn     func(ctx context.Context, conn net.Conn, spec internalapp.RemoteDeviceSpec) (domain.PortID, error)
	detachPortFn       func(ctx context.Context, id domain.PortID) error
	listPortsFn        func(ctx context.Context) ([]domain.Port, error)
	modulesAvailableFn func(ctx context.Context) error
}

func (s *stubImporterKernel) AttachRemote(ctx context.Context, conn net.Conn, spec internalapp.RemoteDeviceSpec) (domain.PortID, error) {
	if s.attachRemoteFn != nil {
		return s.attachRemoteFn(ctx, conn, spec)
	}

	return 0, nil
}

func (s *stubImporterKernel) DetachPort(ctx context.Context, id domain.PortID) error {
	if s.detachPortFn != nil {
		return s.detachPortFn(ctx, id)
	}

	return nil
}

func (s *stubImporterKernel) ListPorts(ctx context.Context) ([]domain.Port, error) {
	if s.listPortsFn != nil {
		return s.listPortsFn(ctx)
	}

	return nil, nil
}

func (s *stubImporterKernel) ModulesAvailable(ctx context.Context) error {
	if s.modulesAvailableFn != nil {
		return s.modulesAvailableFn(ctx)
	}

	return nil
}

// stubKernelEvents is a hand-rolled stand-in for app.KernelEvents.
// Subscribe returns a fresh never-closing channel and a no-op cancel
// unless a subscribeFn override is wired in.
type stubKernelEvents struct {
	subscribeFn func(ctx context.Context) (<-chan domain.Event, func(), error)
}

func (s *stubKernelEvents) Subscribe(ctx context.Context) (<-chan domain.Event, func(), error) {
	if s.subscribeFn != nil {
		return s.subscribeFn(ctx)
	}

	ch := make(chan domain.Event)

	return ch, func() { close(ch) }, nil
}

// stubTransport is a hand-rolled stand-in for app.Transport. Dial
// returns a pair of net.Pipe conns so the codec has something to
// write to / read from without opening a real socket.
type stubTransport struct {
	dialFn   func(ctx context.Context, endpoint domain.RemoteEndpoint) (net.Conn, error)
	listenFn func(ctx context.Context, addr string) (net.Listener, error)
}

func (s *stubTransport) Dial(ctx context.Context, endpoint domain.RemoteEndpoint) (net.Conn, error) {
	if s.dialFn != nil {
		return s.dialFn(ctx, endpoint)
	}

	// Return a connected pipe pair; drain the peer half so writes to
	// the returned conn do not deadlock in tests that stub a
	// happy-path decode.
	local, remote := net.Pipe()

	go func() {
		// Read side of the remote pair; drains whatever the codec
		// writes so the local side's Write never parks. Exits when
		// remote is closed (e.g. by the test tearing down).
		buf := make([]byte, 64)
		for {
			_, err := remote.Read(buf)
			if err != nil {
				_ = remote.Close()

				return
			}
		}
	}()

	return local, nil
}

func (s *stubTransport) Listen(ctx context.Context, addr string) (net.Listener, error) {
	if s.listenFn != nil {
		return s.listenFn(ctx, addr)
	}

	return nil, errNotImplemented
}

// stubCodec is a hand-rolled stand-in for app.ProtocolCodec. Each
// method returns a zero value unless the matching Fn is set. Encode
// methods write nothing by default so the fake Dial drain goroutine
// does not loop.
type stubCodec struct {
	encodeOpReqDevlistFn  func() []byte
	encodeOpReqImportFn   func(w io.Writer, busID domain.BusID) error
	encodeOpRepDevlistFn  func(w io.Writer, devs []domain.Device) error
	encodeOpRepImportFn   func(w io.Writer, d domain.Device) error
	decodeHeaderFn        func(r io.Reader) (uint16, wire.OpCode, uint32, error)
	decodeOpRepDevlistFn  func(r io.Reader) ([]domain.Device, error)
	decodeOpReqImportFn   func(r io.Reader) (domain.BusID, error)
	decodeOpRepImportFn   func(r io.Reader) (domain.Device, error)
}

func (s *stubCodec) EncodeOpReqDevlist() []byte {
	if s.encodeOpReqDevlistFn != nil {
		return s.encodeOpReqDevlistFn()
	}

	return []byte{}
}

func (s *stubCodec) EncodeOpReqImport(w io.Writer, busID domain.BusID) error {
	if s.encodeOpReqImportFn != nil {
		return s.encodeOpReqImportFn(w, busID)
	}

	return nil
}

func (s *stubCodec) EncodeOpRepDevlist(w io.Writer, devs []domain.Device) error {
	if s.encodeOpRepDevlistFn != nil {
		return s.encodeOpRepDevlistFn(w, devs)
	}

	return nil
}

func (s *stubCodec) EncodeOpRepImport(w io.Writer, d domain.Device) error {
	if s.encodeOpRepImportFn != nil {
		return s.encodeOpRepImportFn(w, d)
	}

	return nil
}

func (s *stubCodec) DecodeHeader(r io.Reader) (uint16, wire.OpCode, uint32, error) {
	if s.decodeHeaderFn != nil {
		return s.decodeHeaderFn(r)
	}

	return 0, 0, 0, nil
}

func (s *stubCodec) DecodeOpRepDevlist(r io.Reader) ([]domain.Device, error) {
	if s.decodeOpRepDevlistFn != nil {
		return s.decodeOpRepDevlistFn(r)
	}

	return nil, nil
}

func (s *stubCodec) DecodeOpReqImport(r io.Reader) (domain.BusID, error) {
	if s.decodeOpReqImportFn != nil {
		return s.decodeOpReqImportFn(r)
	}

	return "", nil
}

func (s *stubCodec) DecodeOpRepImport(r io.Reader) (domain.Device, error) {
	if s.decodeOpRepImportFn != nil {
		return s.decodeOpRepImportFn(r)
	}

	return domain.Device{}, nil
}
