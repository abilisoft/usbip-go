// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// BusID is the stable USB topology identifier (e.g. "1-1.2").
//
// On-wire max size is 32 bytes (BusIDSize); we enforce length < BusIDSize
// to leave room for a trailing NUL byte when serialized.
type BusID string

// busIDPattern matches the Linux USB topology syntax: one or more
// decimal digits (the bus number), a dash, then a dot-separated sequence
// of decimal numbers (the port path). v1 contract §4.1 defines this shape.
var busIDPattern = regexp.MustCompile(`^[0-9]+-[0-9]+(\.[0-9]+)*$`)

// ParseBusID validates s and returns a BusID.
//
// Rejects: empty strings, all-whitespace strings, strings with length
// >= BusIDSize, strings containing NUL bytes, strings with leading or
// trailing whitespace, and strings that do not match the Linux USB
// topology pattern ^[0-9]+-[0-9]+(\.[0-9]+)*$.
func ParseBusID(s string) (BusID, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty", ErrBusIDInvalid)
	}

	if strings.TrimSpace(s) == "" {
		return "", fmt.Errorf("%w: whitespace-only", ErrBusIDInvalid)
	}

	if len(s) >= BusIDSize {
		return "", fmt.Errorf("%w: length %d exceeds %d", ErrBusIDInvalid, len(s), BusIDSize-1)
	}

	if strings.ContainsRune(s, '\x00') {
		return "", fmt.Errorf("%w: contains NUL byte", ErrBusIDInvalid)
	}

	if !busIDPattern.MatchString(s) {
		return "", fmt.Errorf("%w: %q does not match topology pattern", ErrBusIDInvalid, s)
	}

	return BusID(s), nil
}

// String returns the busid as a string.
func (b BusID) String() string { return string(b) }

// IsValid reports whether b is a non-zero, well-formed busid. Uses the
// same acceptance rules as ParseBusID. This makes BusID values
// constructed directly (bypassing ParseBusID) detectable via IsValid.
func (b BusID) IsValid() bool {
	if b == "" {
		return false
	}

	if len(b) >= BusIDSize {
		return false
	}

	s := string(b)
	if strings.ContainsRune(s, '\x00') {
		return false
	}

	if strings.TrimSpace(s) == "" {
		return false
	}

	return busIDPattern.MatchString(s)
}

// ValidateWireBusID applies the wire-receive acceptance check: the
// field must be non-empty, shorter than the 32-byte field, and every
// byte must fall inside the sysfs-basename charset [A-Za-z0-9._-].
// The charset is narrower than "printable ASCII" on purpose — peer
// busids flow directly into path.Join when the exporter opens the
// per-device sysfs attribute files, so any byte that could escape a
// basename (notably '/', but also whitespace and control bytes) is a
// traversal hazard. The charset still covers every real-world shape,
// including the vudc name "usbip-vudc.0"; user-facing entry points
// continue to route through ParseBusID's stricter topology check.
func ValidateWireBusID(s string) (BusID, error) {
	if s == "" {
		return "", fmt.Errorf("%w: empty", ErrBusIDInvalid)
	}

	if len(s) >= BusIDSize {
		return "", fmt.Errorf("%w: length %d exceeds %d", ErrBusIDInvalid, len(s), BusIDSize-1)
	}

	for _, r := range s {
		if !isWireBusIDRune(r) {
			return "", fmt.Errorf("%w: %q contains disallowed byte %q", ErrBusIDInvalid, s, r)
		}
	}

	return BusID(s), nil
}

// isWireBusIDRune reports whether r is a permitted byte in a wire
// busid: ASCII letter, digit, '.', '_', or '-'. Everything else —
// including NUL, control bytes, whitespace, and path separators — is
// rejected at the wire boundary.
func isWireBusIDRune(r rune) bool {
	isAlpha := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	isDigit := r >= '0' && r <= '9'
	isPunct := r == '.' || r == '_' || r == '-'

	return isAlpha || isDigit || isPunct
}
