// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
)

// SessionID is a 16-byte UUIDv7 identifying a single client connection.
// The UUIDv7 layout gives sessions a natural chronological ordering.
type SessionID [16]byte

// NewSessionID returns a freshly-generated UUIDv7. It returns an error
// when the underlying randomness source fails.
func NewSessionID() (SessionID, error) {
	u, err := uuid.NewV7()
	if err != nil {
		return SessionID{}, fmt.Errorf("generate session id: %w", err)
	}

	return SessionID(u), nil
}

// String returns the canonical 36-character hyphenated UUID form.
func (id SessionID) String() string { return uuid.UUID(id).String() }

// Session describes a single client connection from the daemon's view.
type Session struct {
	// ID is the unique session identifier (UUIDv7).
	ID SessionID
	// RemoteAddr is the peer address+port as observed by the daemon.
	RemoteAddr netip.AddrPort
	// BusID is the exported device's busid for this session.
	BusID BusID
	// StartedAt is the wall-clock time at which the session handshake completed.
	StartedAt time.Time
	// BytesIn counts bytes received from the peer.
	BytesIn uint64
	// BytesOut counts bytes transmitted to the peer.
	BytesOut uint64
}
