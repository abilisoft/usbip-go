package app

import "errors"

// Sentinel errors returned by internal/app services. Consumers in the
// public facade (pkg/usbip) re-export a subset; the rest are internal
// signalling between the service layer and its callers.
var (
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

	// ErrPortDetached is the seed error handed to the reconnect
	// watcher's OnReconnect callback on the FIRST attempt: the attach
	// succeeded but the port was subsequently torn down. Wrapped by
	// the watcher with the port id and detection source context.
	ErrPortDetached = errors.New("port detached")
)
