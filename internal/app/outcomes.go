// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"

	"golang.org/x/sys/unix"
)

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

// SysfsWritePath identifies the closed set of sysfs writes the kernel
// adapter performs. Used as a structured log field on write failures so
// operators can query journald by path category.
type SysfsWritePath string

// SysfsWritePath values.
const (
	SysfsWritePathBind        SysfsWritePath = "bind"
	SysfsWritePathUnbind      SysfsWritePath = "unbind"
	SysfsWritePathMatchBusID  SysfsWritePath = "match_busid"
	SysfsWritePathRebind      SysfsWritePath = "rebind"
	SysfsWritePathAttach      SysfsWritePath = "attach"
	SysfsWritePathDetach      SysfsWritePath = "detach"
	SysfsWritePathUsbipSockfd SysfsWritePath = "usbip_sockfd"
	SysfsWritePathOther       SysfsWritePath = "other"
)

// SysfsErrno classifies a sysfs write failure into a closed set of
// POSIX errnos. Used as a structured log field so journald queries
// can filter by errno without parsing free-form error strings.
type SysfsErrno string

// SysfsErrno values.
const (
	SysfsErrnoENOENT SysfsErrno = "ENOENT"
	SysfsErrnoEACCES SysfsErrno = "EACCES"
	SysfsErrnoEPERM  SysfsErrno = "EPERM"
	SysfsErrnoEBUSY  SysfsErrno = "EBUSY"
	SysfsErrnoENODEV SysfsErrno = "ENODEV"
	SysfsErrnoEIO    SysfsErrno = "EIO"
	SysfsErrnoOther  SysfsErrno = "other"
)

// SysfsErrnoFromError collapses an arbitrary error into the closed
// SysfsErrno set. Uses errors.As to walk the chain so wrapped errnos
// (fmt.Errorf("...%w...", unix.EACCES)) still classify correctly; any
// non-errno error returns SysfsErrnoOther.
func SysfsErrnoFromError(err error) SysfsErrno {
	if err == nil {
		return SysfsErrnoOther
	}

	var errno unix.Errno
	if !errors.As(err, &errno) {
		return SysfsErrnoOther
	}

	switch errno {
	case unix.ENOENT:
		return SysfsErrnoENOENT
	case unix.EACCES:
		return SysfsErrnoEACCES
	case unix.EPERM:
		return SysfsErrnoEPERM
	case unix.EBUSY:
		return SysfsErrnoEBUSY
	case unix.ENODEV:
		return SysfsErrnoENODEV
	case unix.EIO:
		return SysfsErrnoEIO
	default:
		return SysfsErrnoOther
	}
}

// SysfsWritePathFromAbs maps an absolute sysfs path to its closed-set
// label. Matching is by suffix on the final path segment. Anything
// that doesn't match collapses to SysfsWritePathOther so ad-hoc paths
// cannot explode log cardinality.
func SysfsWritePathFromAbs(path string) SysfsWritePath {
	switch {
	case hasSysfsSuffix(path, "/usbip-host/bind"):
		return SysfsWritePathBind
	case hasSysfsSuffix(path, "/usbip-host/unbind"):
		return SysfsWritePathUnbind
	case hasSysfsSuffix(path, "/usbip-host/match_busid"):
		return SysfsWritePathMatchBusID
	case hasSysfsSuffix(path, "/usbip-host/rebind"):
		return SysfsWritePathRebind
	case hasSysfsSuffix(path, "/vhci_hcd.0/attach"):
		return SysfsWritePathAttach
	case hasSysfsSuffix(path, "/vhci_hcd.0/detach"):
		return SysfsWritePathDetach
	case hasSysfsSuffix(path, "/usbip_sockfd"):
		return SysfsWritePathUsbipSockfd
	default:
		return SysfsWritePathOther
	}
}

func hasSysfsSuffix(path, suffix string) bool {
	if len(path) < len(suffix) {
		return false
	}

	return path[len(path)-len(suffix):] == suffix
}

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
