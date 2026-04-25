// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"strings"
)

// Exit codes — usbipd-go shares the §7.4 catalog with usbip-go where it
// overlaps (0 OK, 1 generic, 2 usage, 9 timeout) and adds one daemon-
// specific code (3 already-running) documented in v1 contract §7.7.
const (
	exitOK           = 0
	exitGeneric      = 1
	exitUsage        = 2
	exitAlreadyRun   = 3
	exitDrainTimeout = 9
)

// cobraUsagePrefixes mirrors cmd/usbip-go's list so an unknown flag lands
// on exitUsage without dragging the whole cmd/usbip-go classifier into
// usbipd-go. Keeping the prefix list local avoids a public package for one
// internal string table.
func cobraUsagePrefixes() []string {
	return []string{
		"unknown flag",
		"unknown shorthand flag",
		"unknown command",
		"invalid argument",
		"flag needs an argument",
		"required flag(s)",
		"accepts ",
		"requires at least",
		"at most",
		"none of the flags",
		"if any flags in the group",
	}
}

// mapError classifies err into its usbipd-go exit code. Drain timeouts are
// surfaced by drain.go directly; this helper handles the generic
// usage/fallback split.
func mapError(err error) int {
	if err == nil {
		return exitOK
	}

	if errors.Is(err, errAlreadyRunning) {
		return exitAlreadyRun
	}

	if errors.Is(err, errDrainTimeout) {
		return exitDrainTimeout
	}

	msg := err.Error()
	for _, prefix := range cobraUsagePrefixes() {
		if strings.Contains(msg, prefix) {
			return exitUsage
		}
	}

	return exitGeneric
}
