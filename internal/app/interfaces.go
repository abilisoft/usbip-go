// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

// Mock-generation directives for matryer/moq. Running
// `go generate ./internal/app/...` emits the fakes into *_mock_test.go
// files that service tests consume. The directives live next to the
// interface declarations so a single go generate bootstraps every
// fake.
//
//go:generate moq -out kernel_importer_mock_test.go -pkg app_test . ImporterKernel
//go:generate moq -out kernel_exporter_mock_test.go -pkg app_test . ExporterKernel
//go:generate moq -out kernel_events_mock_test.go -pkg app_test . KernelEvents
//go:generate moq -out codec_mock_test.go -pkg app_test . ProtocolCodec
//go:generate moq -out transport_mock_test.go -pkg app_test . Dialer

import (
	"context"
	"io"
	"net"

	"github.com/abilisoft/usbip-go/internal/protocol"
	"github.com/abilisoft/usbip-go/pkg/domain"
)

// ImporterKernel wraps the vhci_hcd module (importer-side kernel
// surface). A process that only imports does NOT need usbip_host
// loaded. See v1 contract §5.1.
type ImporterKernel interface {
	AttachRemote(ctx context.Context, conn net.Conn, spec RemoteDeviceSpec) (domain.PortID, error)
	DetachPort(ctx context.Context, id domain.PortID) error
	ListPorts(ctx context.Context) ([]domain.Port, error)
	// ModulesAvailable probes vhci_hcd + usbip_core.
	ModulesAvailable(ctx context.Context) error
}

// ExporterKernel wraps the usbip_host module (exporter-side kernel
// surface). A process that only exports does NOT need vhci_hcd
// loaded. See v1 contract §5.1.
type ExporterKernel interface {
	// ListLocalDevices returns every USB device on the host regardless
	// of bind state — the CLI's `list` view shows the whole bus.
	ListLocalDevices(ctx context.Context) ([]domain.Device, error)

	// ListExportedDevices returns only devices currently bound to
	// usbip-host AND not actively claimed by an importer (SDEV_ST_USED
	// excluded). This is the wire-facing OP_REP_DEVLIST view: peers
	// only see what they could actually attach.
	ListExportedDevices(ctx context.Context) ([]domain.Device, error)

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
// Subscribe semantics (v1 contract §5.1):
//   - each call returns a fresh buffered channel
//   - slow subscribers drop events (logged) rather than back-pressuring fan-out
//   - the returned cancel func unsubscribes and closes the channel
//   - first Subscribe starts the netlink listener; last Unsubscribe stops it
type KernelEvents interface {
	Subscribe(ctx context.Context) (events <-chan domain.Event, cancel func(), err error)
}

// ProtocolCodec is the wire-level USBIP codec surface the app layer
// depends on. It mirrors the method set on internal/adapter/wire.Codec.
// v1 contract §5.1 declares the interface; the wire package's Codec struct
// provides the implementation.
type ProtocolCodec interface {
	EncodeOpReqDevlist() []byte
	EncodeOpReqImport(w io.Writer, busID domain.BusID) error
	EncodeOpRepDevlist(w io.Writer, devs []domain.Device) error
	EncodeOpRepImport(w io.Writer, d domain.Device) error
	EncodeOpRepImportError(w io.Writer, status uint32) error
	DecodeHeader(r io.Reader) (version uint16, op protocol.OpCode, status uint32, err error)
	DecodeOpRepDevlist(r io.Reader) ([]domain.Device, error)
	DecodeOpReqImport(r io.Reader) (domain.BusID, error)
	DecodeOpReqImportBody(r io.Reader) (domain.BusID, error)
	DecodeOpRepImport(r io.Reader) (domain.Device, error)
}

// Dialer abstracts importer-side TCP connection establishment.
// Production uses internal/adapter/transport; tests inject a fake.
type Dialer interface {
	Dial(ctx context.Context, endpoint domain.RemoteEndpoint, opts TransportOptions) (net.Conn, error)
}

// Transport preserves the internal name used by existing option wiring while
// exposing only the capability the importer actually consumes.
type Transport = Dialer
