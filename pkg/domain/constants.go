// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import "time"

// DefaultPort is the conventional USB/IP port. IANA registered TCP/3240
// for "Trio Motion Control Port" in 2002; USB/IP's use of the same port
// is a de-facto convention predating most implementations, and changing
// it would break interop with upstream usbip-utils, usbipd-win, and every
// other implementation.
const (
	DefaultPort     uint16 = 3240
	DefaultEndpoint        = "0.0.0.0:3240"

	// ProtocolVersion is the USBIP protocol version (matches kernel
	// include/uapi/linux/usbip.h). USBIP 1.1.1.
	ProtocolVersion uint16 = 0x0111

	// BusIDSize is the wire-format busid field length.
	BusIDSize = 32
	// SysPathSize is the wire-format sysfs-path field length.
	SysPathSize = 256

	// DefaultDialTimeout is the recommended dial budget for callers that want
	// an explicit bound. The transport zero value deliberately uses net.Dialer's
	// platform default and does not apply this constant automatically.
	DefaultDialTimeout = 10 * time.Second
	// DefaultHandshakeTimeout is the legacy recommended handshake budget.
	//
	// Deprecated: use DefaultExporterHandshakeTimeout for the runtime default or
	// configure an explicit timeout for the relevant operation.
	DefaultHandshakeTimeout = 5 * time.Second
	// DefaultExporterHandshakeTimeout is the Exporter's USB/IP OP handshake bound.
	DefaultExporterHandshakeTimeout = 10 * time.Second
	// DefaultShutdownTimeout is the CLI daemon's default graceful-shutdown budget.
	// Library callers control shutdown with their context or explicit options.
	DefaultShutdownTimeout = 30 * time.Second
)
