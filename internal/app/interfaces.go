package app

import (
	"context"
	"io"
	"net"

	"github.com/abilisoft/usbip-go/internal/adapter/wire"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// ImporterKernel wraps the vhci_hcd module (importer-side kernel
// surface). A process that only imports does NOT need usbip_host
// loaded. See spec §5.1.
type ImporterKernel interface {
	AttachRemote(ctx context.Context, conn net.Conn, spec RemoteDeviceSpec) (domain.PortID, error)
	DetachPort(ctx context.Context, id domain.PortID) error
	ListPorts(ctx context.Context) ([]domain.Port, error)
	// ModulesAvailable probes vhci_hcd + usbip_core.
	ModulesAvailable(ctx context.Context) error
}

// ExporterKernel wraps the usbip_host module (exporter-side kernel
// surface). A process that only exports does NOT need vhci_hcd
// loaded. See spec §5.1.
type ExporterKernel interface {
	ListLocalDevices(ctx context.Context) ([]domain.Device, error)
	Bind(ctx context.Context, busID domain.BusID) error
	Unbind(ctx context.Context, busID domain.BusID) error
	ExportOnConn(ctx context.Context, conn net.Conn, busID domain.BusID) error
	// Disconnect writes -1 to usbip_sockfd to drop an active session.
	Disconnect(ctx context.Context, busID domain.BusID) error
	// ModulesAvailable probes usbip_host + usbip_core.
	ModulesAvailable(ctx context.Context) error
}

// KernelEvents is the shared uevent source consumed by the importer
// reconnect watcher, the public Watch/WatchSessions streams, and
// tests. One netlink socket, many consumers via an internal fan-out.
//
// Subscribe semantics (spec §5.1):
//   - each call returns a fresh buffered channel
//   - slow subscribers drop events (logged) rather than back-pressuring fan-out
//   - the returned cancel func unsubscribes and closes the channel
//   - first Subscribe starts the netlink listener; last Unsubscribe stops it
type KernelEvents interface {
	Subscribe(ctx context.Context) (events <-chan domain.Event, cancel func(), err error)
}

// ProtocolCodec is the wire-level USBIP codec surface the app layer
// depends on. It mirrors the method set on internal/adapter/wire.Codec.
// Spec §5.1 declares the interface; the wire package's Codec struct
// provides the implementation.
type ProtocolCodec interface {
	EncodeOpReqDevlist() []byte
	EncodeOpReqImport(w io.Writer, busID domain.BusID) error
	EncodeOpRepDevlist(w io.Writer, devs []domain.Device) error
	EncodeOpRepImport(w io.Writer, d domain.Device) error
	DecodeHeader(r io.Reader) (version uint16, op wire.OpCode, status uint32, err error)
	DecodeOpRepDevlist(r io.Reader) ([]domain.Device, error)
	DecodeOpReqImport(r io.Reader) (domain.BusID, error)
	DecodeOpRepImport(r io.Reader) (domain.Device, error)
}

// Transport abstracts the TCP transport used by importer and exporter.
// Production uses internal/adapter/transport; tests inject a fake.
type Transport interface {
	Dial(ctx context.Context, endpoint domain.RemoteEndpoint) (net.Conn, error)
	Listen(ctx context.Context, addr string) (net.Listener, error)
}

// Compile-time assertion: wire.Codec satisfies ProtocolCodec. If this
// line fails to build, either the wire package drifted from the
// interface or the interface changed shape without an adapter update;
// either way the drift must be fixed — do NOT silently relax this
// assertion.
var _ ProtocolCodec = (*wire.Codec)(nil)
