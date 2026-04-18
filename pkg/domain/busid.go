package domain

import (
	"fmt"
	"strings"
)

// BusID is the stable USB topology identifier (e.g. "1-1.2").
//
// On-wire max size is 32 bytes (BusIDSize); we enforce length < BusIDSize
// to leave room for a trailing NUL byte when serialized.
type BusID string

// ParseBusID validates s and returns a BusID.
//
// Rejects: empty strings, all-whitespace strings, strings with length
// >= BusIDSize, and strings containing NUL bytes.
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

	return BusID(s), nil
}

// String returns the busid as a string.
func (b BusID) String() string { return string(b) }

// IsValid reports whether b is a non-zero busid with valid size and no NUL.
func (b BusID) IsValid() bool {
	if b == "" {
		return false
	}

	if len(b) >= BusIDSize {
		return false
	}

	if strings.ContainsRune(string(b), '\x00') {
		return false
	}

	if strings.TrimSpace(string(b)) == "" {
		return false
	}

	return true
}
