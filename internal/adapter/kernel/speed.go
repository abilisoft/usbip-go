// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package kernel

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// ErrSysfsSpeedUnrecognized is returned by ReadSpeedAttr when the
// kernel emitted a string outside speed_show()'s known output set.
// Surfacing as a typed sentinel lets callers distinguish "kernel
// reported a value we do not yet map" from a generic I/O error.
var ErrSysfsSpeedUnrecognized = errors.New("unrecognized sysfs speed")

// sysfsMbpsToSpeed maps the Mbps strings emitted by
// /sys/bus/usb/devices/<id>/speed (Linux drivers/usb/core/sysfs.c
// speed_show) to domain.Speed enum values. The kernel emits Mbps, not
// the usb_device_speed enum integer, so a raw uint cast produces the
// wrong value for every speed above Full (e.g. sysfs "5000" ≠ enum 5).
// Declared package-scope because Go has no native const-map; the
// gochecknoglobals exclusion in .golangci.yml scopes the variance to
// this single file.
var sysfsMbpsToSpeed = map[string]domain.Speed{
	"unknown": domain.SpeedUnknown, // speed_show()'s default branch: device pre-enumeration / USB_SPEED_UNKNOWN
	"1.5":     domain.SpeedLow,
	"12":      domain.SpeedFull,
	"480":     domain.SpeedHigh,
	"5000":    domain.SpeedSuper,
	"10000":   domain.SpeedSuperPlus,
	"20000":   domain.SpeedSuperPlus, // USB 3.2 Gen 2x2 (USB_SPEED_SUPER_PLUS, ssp_rate=GEN_2x2)
}

// ReadSpeedAttr reads the sysfs "speed" attribute at path and converts
// the Mbps string to a domain.Speed enum value. Returns
// ErrSysfsSpeedUnrecognized for any string not in the kernel's
// speed_show() output set.
func ReadSpeedAttr(fsys fs.FS, path string) (domain.Speed, error) {
	line, err := ReadLine(fsys, path)
	if err != nil {
		return 0, err
	}

	speed, ok := sysfsMbpsToSpeed[line]
	if !ok {
		return 0, fmt.Errorf("%w %q at %q", ErrSysfsSpeedUnrecognized, line, path)
	}

	return speed, nil
}
