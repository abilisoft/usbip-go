package usbip

import (
	"encoding/json"
	"fmt"
)

// ModuleState is the tri-state classification of a USB/IP kernel
// module probe result. Added by Phase 8 review Finding 5: the previous
// two-state "loaded" / "missing" design silently collapsed EACCES /
// EIO / any-other-error into "missing", masking the difference between
// "probe was blocked" and "probe proved the module is absent".
//
// JSON marshaling emits the lowercase string form so the §7.7 status
// schema retains its stable `{"usbip_core": "loaded"}` shape.
type ModuleState int

// ModuleState constants. Order is public API: the iota placement
// determines what the zero value means. ModuleStateUnknown is 0 so a
// forgotten-to-populate map entry flags as Unknown rather than the
// potentially misleading Loaded or Missing.
const (
	ModuleStateUnknown ModuleState = iota
	ModuleStateLoaded
	ModuleStateMissing
)

// String returns the lowercase wire form of the state. Invalid
// (out-of-range) values render as "unknown" for a safe fallback.
func (s ModuleState) String() string {
	switch s {
	case ModuleStateLoaded:
		return "loaded"
	case ModuleStateMissing:
		return "missing"
	case ModuleStateUnknown:
		return "unknown"
	}

	return "unknown"
}

// MarshalJSON renders the lowercase string form so the status JSON
// schema stays a stable string mapping. json.Marshaler is the right
// seam here because the caller's map[string]ModuleState should
// otherwise marshal as an integer.
func (s ModuleState) MarshalJSON() ([]byte, error) {
	data, err := json.Marshal(s.String())
	if err != nil {
		return nil, fmt.Errorf("marshal ModuleState: %w", err)
	}

	return data, nil
}
