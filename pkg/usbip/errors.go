package usbip

import (
	"errors"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// Sentinel errors re-exported from pkg/domain. Assignment — not wrapping
// — preserves identity so errors.Is(err, usbip.ErrX) and
// errors.Is(err, domain.ErrX) match the same underlying value. Spec §5.7.
var (
	// ErrDeviceNotFound indicates the requested busid is not present.
	ErrDeviceNotFound = domain.ErrDeviceNotFound

	// ErrDeviceAlreadyBound indicates the device is already bound to
	// usbip-host and cannot be bound again.
	ErrDeviceAlreadyBound = domain.ErrDeviceAlreadyBound

	// ErrDeviceNotBound indicates the device is not currently bound to
	// usbip-host.
	ErrDeviceNotBound = domain.ErrDeviceNotBound

	// ErrPortInUse indicates a local vhci port is already attached.
	ErrPortInUse = domain.ErrPortInUse

	// ErrNoFreePort indicates no vhci port is available for attach.
	ErrNoFreePort = domain.ErrNoFreePort

	// ErrProtocolMismatch indicates the peer reported a different USBIP
	// protocol version.
	ErrProtocolMismatch = domain.ErrProtocolMismatch

	// ErrProtocolError indicates the peer replied with a protocol error
	// status code (OP_REP_*.status != 0 on a handshake frame).
	ErrProtocolError = domain.ErrProtocolError

	// ErrBusIDInvalid indicates a bus id failed validation.
	ErrBusIDInvalid = domain.ErrBusIDInvalid

	// ErrPermission indicates the caller lacks the privileges needed
	// (typically CAP_SYS_ADMIN or root).
	ErrPermission = domain.ErrPermission

	// ErrKernelModuleMissing indicates a required kernel module
	// (usbip-core/usbip-host/vhci-hcd) is not loaded.
	ErrKernelModuleMissing = domain.ErrKernelModuleMissing

	// ErrAlreadyRunning indicates another exporter instance is holding
	// the shared PID lock.
	ErrAlreadyRunning = domain.ErrAlreadyRunning

	// ErrAlreadyShutdown indicates the exporter has been shut down and
	// cannot accept further requests. Aliased to pkg/domain because the
	// domain package already publishes this lifecycle sentinel on the
	// public surface (spec §5.7).
	ErrAlreadyShutdown = domain.ErrAlreadyShutdown
)

// Public lifecycle sentinels. ErrImporterClosed and
// ErrServeAlreadyRunning carry identities distinct from the
// internal/app sentinels of the same name so downstream callers never
// need to import internal/app to classify an error; the forwarding
// methods in usbip.go translate the matching internal identity into
// these values. ErrExporterShutdown aliases the domain-level
// ErrAlreadyShutdown so the spec-listed public name stays matchable
// via errors.Is on either alias.
var (
	// ErrImporterClosed indicates a method was called after Close.
	// Close itself is idempotent (returns nil); other operations on a
	// closed Importer surface this sentinel via errors.Is.
	ErrImporterClosed = errors.New("importer closed")

	// ErrExporterShutdown indicates Serve (or a Serve-adjacent method)
	// was called after Shutdown completed. Shutdown is terminal: the
	// caller must construct a new Exporter to serve again. Aliased to
	// domain.ErrAlreadyShutdown so errors.Is against either form
	// succeeds.
	ErrExporterShutdown = domain.ErrAlreadyShutdown

	// ErrServeAlreadyRunning indicates Serve was called while another
	// Serve is still active on the same Exporter. Overlapping accept
	// loops would fight over the shared session bookkeeping, so the
	// second call is rejected.
	ErrServeAlreadyRunning = errors.New("exporter: Serve already running")
)
