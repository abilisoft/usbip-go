// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

// Domain outcome enums. These name the closed set of outcomes for each
// operation in the project's ubiquitous language. They are emitted as
// `slog.String("outcome", string(outcome))` at every operation boundary
// so structured journald queries (`journalctl -u usbipd --output=json`)
// can filter by outcome without parsing free-form messages.

// SessionOutcome classifies how an inbound exporter handshake resolved.
type SessionOutcome string

// SessionOutcome values.
const (
	OutcomeHandshakeOK     SessionOutcome = "handshake_ok"
	OutcomeRejectedACL     SessionOutcome = "rejected_acl"
	OutcomeRejectedRate    SessionOutcome = "rejected_rate"
	OutcomeRejectedCap     SessionOutcome = "rejected_cap"
	OutcomeHandshakeFailed SessionOutcome = "handshake_failed"
)

// HandshakeOp identifies which exporter handshake opcode was served.
type HandshakeOp string

// HandshakeOp values.
const (
	HandshakeOpDevlist HandshakeOp = "devlist"
	HandshakeOpImport  HandshakeOp = "import"
)

// BindOutcome classifies how an Exporter.Bind resolved.
type BindOutcome string

// BindOutcome values.
const (
	BindOutcomeOK           BindOutcome = "ok"
	BindOutcomeAlreadyBound BindOutcome = "already_bound"
	BindOutcomeNotFound     BindOutcome = "not_found"
	BindOutcomePermission   BindOutcome = "permission"
	BindOutcomeError        BindOutcome = "error"
)

// UnbindOutcome classifies how an Exporter.Unbind resolved.
type UnbindOutcome string

// UnbindOutcome values.
const (
	UnbindOutcomeOK         UnbindOutcome = "ok"
	UnbindOutcomeNotBound   UnbindOutcome = "not_bound"
	UnbindOutcomePermission UnbindOutcome = "permission"
	UnbindOutcomeError      UnbindOutcome = "error"
)

// DisconnectReason classifies why a Session ended.
type DisconnectReason string

// DisconnectReason values.
const (
	DisconnectReasonGraceful    DisconnectReason = "graceful"
	DisconnectReasonClientGone  DisconnectReason = "client_gone"
	DisconnectReasonKernelError DisconnectReason = "kernel_error"
	DisconnectReasonShutdown    DisconnectReason = "shutdown"
)

// AttachOutcome classifies how an Importer.Attach resolved.
type AttachOutcome string

// AttachOutcome values.
const (
	AttachOutcomeOK               AttachOutcome = "ok"
	AttachOutcomePermission       AttachOutcome = "permission"
	AttachOutcomeNoFreePort       AttachOutcome = "no_free_port"
	AttachOutcomeProtocolMismatch AttachOutcome = "protocol_mismatch"
	AttachOutcomeDialFailed       AttachOutcome = "dial_failed"
	AttachOutcomeKernelError      AttachOutcome = "kernel_error"
)

// DetachOutcome classifies how an Importer.Detach resolved.
type DetachOutcome string

// DetachOutcome values.
const (
	DetachOutcomeOK       DetachOutcome = "ok"
	DetachOutcomeNotFound DetachOutcome = "not_found"
	DetachOutcomeError    DetachOutcome = "error"
)

// ReconnectOutcome classifies how a single reconnect-watcher attempt
// resolved.
type ReconnectOutcome string

// ReconnectOutcome values.
const (
	ReconnectOutcomeOK        ReconnectOutcome = "ok"
	ReconnectOutcomeBackoff   ReconnectOutcome = "backoff"
	ReconnectOutcomeExhausted ReconnectOutcome = "exhausted"
	ReconnectOutcomeCanceled  ReconnectOutcome = "canceled"
)

// KernelModule names the kernel modules whose load state the daemon
// reports through readiness checks. Closed set.
type KernelModule string

// KernelModule values.
const (
	ModuleUsbipCore KernelModule = "usbip_core"
	ModuleVhciHcd   KernelModule = "vhci_hcd"
	ModuleUsbipHost KernelModule = "usbip_host"
	ModuleUsbipVudc KernelModule = "usbip_vudc"
)
