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
// of decimal numbers (the port path). Spec §4.1 defines this shape.
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
