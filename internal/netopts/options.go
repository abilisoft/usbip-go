// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

// Package netopts holds the TransportOptions value type and its
// validation. It exists to break the import cycle between
// `internal/app` (which declares the consumer-defined Transport
// interface) and `internal/adapter/transport` (which implements it).
//
// The DDD layering rule forbids `internal/app` from importing
// `internal/adapter/transport`, so the value type that flows through
// the Transport interface signature lives here — a leaf package both
// callers can import without cycling.
//
// Field semantics are documented on the type itself; PR 1a wires the
// type through the interface, PR 1b adds the adapter-side tuning logic
// that consumes non-zero values.
package netopts

import (
	"errors"
	"fmt"
	"time"
)

// ErrTransportOptionsInvalid indicates a TransportOptions field
// carried a negative value (durations, probe count, or buffer size).
// Surfaced at constructor time so the caller cannot build a working
// Importer/Exporter with malformed options.
var ErrTransportOptionsInvalid = errors.New("netopts: TransportOptions invalid")

// TransportOptions carries TCP-level tuning knobs for the transport
// adapter that backs Importer and Exporter. Each field's zero value
// means "inherit current behavior / kernel default", so an unset
// TransportOptions struct keeps v1.0.0 wire behavior unchanged.
type TransportOptions struct {
	// DialConnectTimeout caps the TCP connect phase on outbound
	// importer dials. Zero defers to net.Dialer's zero-value behavior
	// (no app-level cap). Negative is rejected by Validate.
	DialConnectTimeout time.Duration

	// TCPKeepAliveIdle is the idle period before TCP keepalive probes
	// start. Zero leaves the OS default in place. Negative rejected.
	TCPKeepAliveIdle time.Duration

	// TCPKeepAliveInterval is the spacing between TCP keepalive
	// probes. Zero leaves the OS default in place. Negative rejected.
	TCPKeepAliveInterval time.Duration

	// TCPKeepAliveProbes is the count of unanswered keepalive probes
	// after which the kernel declares the connection dead. Zero leaves
	// the OS default in place. Negative rejected.
	TCPKeepAliveProbes int

	// SendBufferBytes is the requested SO_SNDBUF on outbound dials and
	// accepted listener connections. Zero inherits the kernel default.
	// Linux doubles SO_SNDBUF internally; adapter-side assertions must
	// allow `actual >= requested`. Negative rejected.
	SendBufferBytes int

	// ReceiveBufferBytes is the requested SO_RCVBUF (same semantics as
	// SendBufferBytes). Negative rejected.
	ReceiveBufferBytes int

	// ReadDeadline is the static read deadline applied to userspace
	// OP_REQ_*/OP_REP_* handshakes; it is cleared before any kernel fd
	// handoff. Zero means no app-level read deadline. Negative
	// rejected.
	ReadDeadline time.Duration

	// WriteDeadline mirrors ReadDeadline for the write side.
	WriteDeadline time.Duration
}

// Validate returns ErrTransportOptionsInvalid wrapped with the
// offending field name if any duration, probe count, or buffer size
// is negative. Zero values are always accepted so a caller can opt
// into individual knobs without specifying every field.
func Validate(opts TransportOptions) error {
	switch {
	case opts.DialConnectTimeout < 0:
		return invalidField("DialConnectTimeout")
	case opts.TCPKeepAliveIdle < 0:
		return invalidField("TCPKeepAliveIdle")
	case opts.TCPKeepAliveInterval < 0:
		return invalidField("TCPKeepAliveInterval")
	case opts.TCPKeepAliveProbes < 0:
		return invalidField("TCPKeepAliveProbes")
	case opts.SendBufferBytes < 0:
		return invalidField("SendBufferBytes")
	case opts.ReceiveBufferBytes < 0:
		return invalidField("ReceiveBufferBytes")
	case opts.ReadDeadline < 0:
		return invalidField("ReadDeadline")
	case opts.WriteDeadline < 0:
		return invalidField("WriteDeadline")
	}

	return nil
}

func invalidField(name string) error {
	return fmt.Errorf("%w: %s must not be negative", ErrTransportOptionsInvalid, name)
}
