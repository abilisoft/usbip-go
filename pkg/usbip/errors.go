package usbip

import "github.com/abilisoft/usbip-go/pkg/domain"

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
)
