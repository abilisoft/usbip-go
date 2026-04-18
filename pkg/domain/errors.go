package domain

import "errors"

// ErrBusIDInvalid is returned when a bus id fails validation
// (empty, whitespace-only, too long, or contains a NUL byte).
//
// Additional sentinels defined by spec §4.4 and §6.2 are added in
// later tasks in the same file.
var ErrBusIDInvalid = errors.New("invalid bus id")
