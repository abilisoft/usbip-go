package app

import (
	"errors"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// Sentinel errors returned by internal/app services. Consumers in the
// public facade (pkg/usbip) re-export a subset; the rest are internal
// signalling between the service layer and its callers.
var (
	// ErrAttachInProgress indicates Attach is already running on the
	// same busid/remote pair. Concurrent Attach calls would race the
	// fd-passing handoff and corrupt the handle map. Aliased to
	// pkg/domain so the public facade re-exports the same identity
	// (pass-2 RANK 6).
	ErrAttachInProgress = domain.ErrAttachInProgress

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

	// ErrAlreadyShutdown indicates Serve was called after Shutdown
	// completed. Shutdown is terminal: callers must construct a new
	// Exporter to serve again.
	ErrAlreadyShutdown = errors.New("exporter already shut down")

	// ErrServeAlreadyRunning indicates Serve was called while another
	// Serve is still active on the same Exporter. Overlapping accept
	// loops would fight over the shared session bookkeeping, so the
	// second call is rejected.
	ErrServeAlreadyRunning = errors.New("exporter: Serve already running")

	// ErrMaxSessionsExceeded indicates the global session cap is full
	// (§11.5.3). Returned from the per-session handler before
	// ExportOnConn so the kernel is never asked to attach past the cap.
	ErrMaxSessionsExceeded = errors.New("max sessions exceeded")

	// ErrPerPeerLimitExceeded indicates the per-source-IP session cap
	// is full (§11.5.3).
	ErrPerPeerLimitExceeded = errors.New("per-peer session limit exceeded")

	// ErrRateLimited indicates the accept-rate token bucket had no
	// tokens available (§11.5.3). The connection is closed without
	// invoking the kernel adapter.
	ErrRateLimited = errors.New("accept rate limit exceeded")

	// ErrHandshakeTooLarge indicates the client sent more than
	// MaxHandshakeBytes before completing the OP request (§11.5.3).
	ErrHandshakeTooLarge = errors.New("handshake payload exceeds max bytes")

	// ErrHandshakeTimeout indicates the client failed to complete its
	// OP request within HandshakeTimeout (§11.5.3).
	ErrHandshakeTimeout = errors.New("handshake timed out")

	// ErrACLRejected indicates the accepted peer's remote IP is not
	// covered by any configured CIDR in the allow-list (§11.5.2).
	ErrACLRejected = errors.New("peer rejected by ACL")

	// ErrACLInvalid indicates one of the WithExporterACL CIDR strings
	// failed to parse. Surfaced at NewExporterWithError time — a Serve-
	// time failure would be a silent security regression.
	ErrACLInvalid = errors.New("invalid ACL CIDR")
)
