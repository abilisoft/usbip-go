// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain

import "strconv"

// Status is a vhci port state matching kernel vdev_status values.
type Status uint32

// Port statuses (kernel vdev_status order).
const (
	StatusNull        Status = 0
	StatusNotAssigned Status = 1
	StatusAvailable   Status = 2
	StatusUsed        Status = 3
	StatusError       Status = 4
)

// String returns a human-readable kebab-case label for s.
// Unknown values return "status(N)" with N in decimal.
func (s Status) String() string {
	switch s {
	case StatusNull:
		return "null"
	case StatusNotAssigned:
		return "not-assigned"
	case StatusAvailable:
		return "available"
	case StatusUsed:
		return "used"
	case StatusError:
		return "error"
	default:
		return "status(" + strconv.FormatUint(uint64(s), 10) + ")"
	}
}
