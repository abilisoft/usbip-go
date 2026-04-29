// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import "errors"

// Sentinel errors returned by pkg/domain and by internal packages that
// surface domain-level conditions. Consumers match via errors.Is.
//
// Message text is a stable part of the public API and MUST NOT be
// changed without a breaking-change bump. List ordering matches spec
// §4.4 followed by the three additions in §6.2.
var (
	// ErrDeviceNotFound indicates the requested busid is not present.
	ErrDeviceNotFound = errors.New("device not found")
	// ErrDeviceAlreadyBound indicates the device is already bound to
	// usbip-host and cannot be bound again.
	ErrDeviceAlreadyBound = errors.New("device already bound")
	// ErrDeviceNotBound indicates the device is not currently bound
	// to usbip-host.
	ErrDeviceNotBound = errors.New("device not bound")
	// ErrPortInUse indicates a local vhci port is already attached.
	ErrPortInUse = errors.New("port in use")
	// ErrNoFreePort indicates no vhci port is available for attach.
	ErrNoFreePort = errors.New("no free vhci port")
	// ErrProtocolMismatch indicates the peer reported a different
	// USBIP protocol version.
	ErrProtocolMismatch = errors.New("usbip protocol version mismatch")
	// ErrProtocolError indicates the peer replied with a protocol
	// error status code (OP_REP_*.status != 0 on a handshake frame).
	ErrProtocolError = errors.New("usbip protocol error reported by peer")
	// ErrAlreadyRunning indicates another exporter instance is holding
	// the shared PID lock.
	ErrAlreadyRunning = errors.New("another instance is already running")
	// ErrAlreadyShutdown indicates the exporter has been shut down and
	// cannot accept further requests.
	ErrAlreadyShutdown = errors.New("exporter already shut down")
	// ErrBusIDInvalid indicates a bus id failed validation.
	ErrBusIDInvalid = errors.New("invalid bus id")
	// ErrPermission indicates the caller lacks the privileges needed
	// (typically CAP_SYS_ADMIN or root).
	ErrPermission = errors.New("operation requires elevated privileges")
	// ErrKernelModuleMissing indicates a required kernel module
	// (usbip-core/usbip-host/vhci-hcd) is not loaded.
	ErrKernelModuleMissing = errors.New("required kernel module not loaded")
	// ErrAttachInProgress indicates Attach is already running for the
	// same (remote, busid) pair. Concurrent Attach calls race the
	// fd-passing handoff and the handle-map insert, so the deduper
	// rejects the second caller with this sentinel (v1 contract §5.5).
	// Hosted on pkg/domain so the public facade can re-export it
	// alongside the other spec-listed sentinels.
	ErrAttachInProgress = errors.New("attach already in progress for this endpoint")
	// ErrUnsupportedDevice indicates the device cannot be exported via
	// usbip — typically a USB hub (bDeviceClass=0x09) or the vhci-hcd
	// loopback device. Distinct from ErrDeviceNotFound (device exists)
	// and ErrBusIDInvalid (id format is valid). Surfaced BEFORE any
	// destructive sysfs write so detaching a hub's drivers does not
	// cascade-disconnect downstream devices.
	ErrUnsupportedDevice = errors.New("device not supported for usbip export")
	// ErrDeviceUnavailable indicates the remote daemon reports the
	// device exists but is currently unusable for import (e.g.
	// stub-side internal error, ST_DEV_ERR=3 from upstream
	// usbip_common.h). Distinct from ErrDeviceAlreadyBound (busy/
	// already attached, ST_DEV_BUSY=2) and ErrDeviceNotFound
	// (ST_NODEV=4).
	ErrDeviceUnavailable = errors.New("remote device unavailable")
)
