// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// Exit codes — authoritative table from v1 contract §7.4. Consumers may grep
// on these values; they form part of the v1 CLI stability contract.
const (
	ExitOK               = 0
	ExitGeneric          = 1
	ExitUsage            = 2
	ExitPermission       = 3
	ExitKernelModule     = 4
	ExitDeviceNotFound   = 5
	ExitDeviceBusy       = 6
	ExitProtocolMismatch = 7
	ExitNetwork          = 8
	ExitTimeout          = 9
	ExitNoFreePort       = 10
	ExitProtocolError    = 11
	// ExitInterrupted signals context.Canceled: the user interrupted
	// via SIGINT or a parent cancelled the call without a deadline.
	// The value follows the Unix SIGINT convention (128 + signal
	// number 2) so shells that surface $? can distinguish an
	// interrupted run from a timeout (exit 9) or a permission fault
	// (exit 3).
	ExitInterrupted = 130
)

// errorEntry pairs a sentinel (matched via errors.Is) with the spec
// §7.4 exit code + stderr template. The registry drives both MapError
// and FormatError so the two functions never drift.
type errorEntry struct {
	sentinel error
	code     int
	format   string
}

// errorRegistry returns the authoritative sentinel → (code, template)
// mapping from v1 contract §7.4. The slice is returned fresh so tests can't
// mutate it.
func errorRegistry() []errorEntry {
	return []errorEntry{
		{
			usbip.ErrPermission, ExitPermission,
			"usbip-go: operation requires elevated privileges (CAP_SYS_ADMIN). Try: sudo usbip-go",
		},
		{
			usbip.ErrKernelModuleMissing, ExitKernelModule,
			"usbip-go: kernel module not loaded. Try: sudo modprobe usbip_core vhci_hcd usbip_host",
		},
		{
			usbip.ErrDeviceNotFound, ExitDeviceNotFound,
			"usbip-go: device not found: %s",
		},
		{
			usbip.ErrDeviceAlreadyBound, ExitDeviceBusy,
			"usbip-go: device is already bound: %s",
		},
		{
			usbip.ErrPortInUse, ExitDeviceBusy,
			"usbip-go: port is in use: %s",
		},
		{
			usbip.ErrDeviceNotBound, ExitDeviceBusy,
			"usbip-go: device is not bound: %s",
		},
		{
			usbip.ErrProtocolMismatch, ExitProtocolMismatch,
			"usbip-go: protocol mismatch: %s",
		},
		{
			usbip.ErrNoFreePort, ExitNoFreePort,
			"usbip-go: no free vhci port available: %s",
		},
		{
			usbip.ErrProtocolError, ExitProtocolError,
			"usbip-go: peer reported an error: %s",
		},
		{
			context.DeadlineExceeded, ExitTimeout,
			"usbip-go: operation timed out: %s",
		},
		{
			context.Canceled, ExitInterrupted,
			"usbip-go: operation interrupted: %s",
		},
	}
}

// MapError classifies err into its exit code per v1 contract §7.4. nil is
// ExitOK; usage-class errors surface ExitUsage; sentinel matches take
// priority over the generic net/timeout detection.
func MapError(err error) int {
	if err == nil {
		return ExitOK
	}

	if isUsageError(err) {
		return ExitUsage
	}

	for _, entry := range errorRegistry() {
		if errors.Is(err, entry.sentinel) {
			return entry.code
		}
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return ExitNetwork
	}

	return ExitGeneric
}

// FormatError renders err as the single-line `usbip-go: ...` stderr message
// prescribed by v1 contract §7.4. The returned string has no trailing newline
// (callers pick the newline policy) and no embedded newlines either —
// errors.Join renders its members newline-separated, so any interior
// newline in the detail text is collapsed to "; " before formatting.
func FormatError(err error) string {
	if err == nil {
		return ""
	}

	// Usage errors are handled by cobra directly; we return the raw
	// error message verbatim to match "we do not override" in §7.4.
	if isUsageError(err) {
		return flattenErrorText(err.Error())
	}

	for _, entry := range errorRegistry() {
		if !errors.Is(err, entry.sentinel) {
			continue
		}

		if strings.Contains(entry.format, "%s") {
			return fmt.Sprintf(entry.format, flattenErrorText(err.Error()))
		}

		return entry.format
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return "usbip-go: network error: " + flattenErrorText(err.Error())
	}

	return "usbip-go: error: " + flattenErrorText(err.Error())
}

// flattenErrorText collapses any embedded newlines in s into "; ". Used
// by FormatError to defend the one-line stderr contract against
// errors.Join chains whose Error() returns newline-separated members.
func flattenErrorText(s string) string {
	// Normalize CRLF first so a Windows-style line separator produces
	// one delimiter, not two.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	return strings.ReplaceAll(s, "\n", "; ")
}

// isUsageError detects the cobra usage-error class. Cobra wraps flag
// parse errors with its own message; matching on the sentinel-like
// prefix is the pragmatic workaround since the wrapper is unexported.
func isUsageError(err error) bool {
	var usageErr *usageError
	if errors.As(err, &usageErr) {
		return true
	}

	msg := err.Error()
	for _, prefix := range cobraUsagePrefixes() {
		if strings.Contains(msg, prefix) {
			return true
		}
	}

	return false
}

// cobraUsagePrefixes lists the substrings cobra emits for its own
// flag/argument parse failures. Keeping them together in one function
// keeps the detection logic grep-able.
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

// usageError is a sentinel wrapper for subcommand-origin usage failures
// (e.g. malformed --backoff spec) that need to exit with ExitUsage even
// though cobra didn't produce the error. Subcommand code wraps a usage-
// class error via errUsage(msg); MapError and isUsageError both
// recognise the wrapped instance.
type usageError struct {
	msg string
}

// Error implements error.
func (e *usageError) Error() string { return e.msg }

// errUsage returns a *usageError so MapError classifies the wrapped
// error as ExitUsage. The string is surfaced verbatim on stderr.
func errUsage(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}
