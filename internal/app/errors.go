package app

import "errors"

// Sentinel errors returned by internal/app services. Consumers in the
// public facade (pkg/usbip) re-export a subset; the rest are internal
// signalling between the service layer and its callers.
var (
	// ErrAutoReconnectNotImplemented is returned by Importer.Attach
	// when AttachOptions.AutoReconnect is true but the watcher
	// goroutine has not been implemented yet. This is a transient
	// sentinel: it is wired up in Phase 5.8 and will disappear from
	// the code-base at that point. Do NOT rely on it from consumer
	// code; it exists only so the Phase 5 Batch A slice can compile
	// and test cleanly.
	ErrAutoReconnectNotImplemented = errors.New("auto-reconnect not implemented; deferred to Task 5.8")

	// ErrAttachInProgress indicates Attach is already running on the
	// same busid/remote pair. Concurrent Attach calls would race the
	// fd-passing handoff and corrupt the handle map.
	ErrAttachInProgress = errors.New("attach already in progress for this endpoint")

	// ErrHandleNotFound indicates Detach or ListPorts was called with
	// a PortID that is not tracked by the Importer. Typical causes:
	// the port was already detached, or the Importer never attached
	// it in the first place.
	ErrHandleNotFound = errors.New("port handle not found")

	// ErrImporterClosed indicates a method was called after Close.
	// Close is idempotent (a second Close returns nil), but other
	// operations on a closed Importer return this sentinel.
	ErrImporterClosed = errors.New("importer closed")
)
