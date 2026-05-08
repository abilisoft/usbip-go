// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package netopts_test

import (
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/netopts"
	"github.com/stretchr/testify/require"
)

// TestValidateZeroValueIsAllowed locks in the inherits-current-behavior
// invariant: a zero-valued TransportOptions struct passes validation
// so callers do not need to populate every field.
func TestValidateZeroValueIsAllowed(t *testing.T) {
	t.Parallel()

	require.NoError(t, netopts.Validate(netopts.TransportOptions{}))
}

// TestValidatePositiveValuesAreAllowed asserts ordinary, in-range
// values pass validation. The check is for negative-only rejection;
// reasonable WAN-class numbers must succeed.
func TestValidatePositiveValuesAreAllowed(t *testing.T) {
	t.Parallel()

	opts := netopts.TransportOptions{
		DialConnectTimeout:   10 * time.Second,
		TCPKeepAliveIdle:     30 * time.Second,
		TCPKeepAliveInterval: 10 * time.Second,
		TCPKeepAliveProbes:   6,
		SendBufferBytes:      256 * 1024,
		ReceiveBufferBytes:   256 * 1024,
		ReadDeadline:         60 * time.Second,
		WriteDeadline:        60 * time.Second,
	}
	require.NoError(t, netopts.Validate(opts))
}

// TestValidateRejectsEachNegativeField table-tests every field for
// negative-value rejection. The error must wrap
// ErrTransportOptionsInvalid so callers can match it via errors.Is
// without parsing the message string.
func TestValidateRejectsEachNegativeField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		opts netopts.TransportOptions
	}{
		{"DialConnectTimeout", netopts.TransportOptions{DialConnectTimeout: -1 * time.Nanosecond}},
		{"TCPKeepAliveIdle", netopts.TransportOptions{TCPKeepAliveIdle: -1 * time.Nanosecond}},
		{"TCPKeepAliveInterval", netopts.TransportOptions{TCPKeepAliveInterval: -1 * time.Nanosecond}},
		{"TCPKeepAliveProbes", netopts.TransportOptions{TCPKeepAliveProbes: -1}},
		{"SendBufferBytes", netopts.TransportOptions{SendBufferBytes: -1}},
		{"ReceiveBufferBytes", netopts.TransportOptions{ReceiveBufferBytes: -1}},
		{"ReadDeadline", netopts.TransportOptions{ReadDeadline: -1 * time.Nanosecond}},
		{"WriteDeadline", netopts.TransportOptions{WriteDeadline: -1 * time.Nanosecond}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := netopts.Validate(tc.opts)
			require.Error(t, err)
			require.ErrorIs(t, err, netopts.ErrTransportOptionsInvalid)
			require.Contains(t, err.Error(), tc.name)
		})
	}
}

// TestValidateErrorWrapsSentinel asserts the returned error reports
// both the offending field name and a wrapped ErrTransportOptionsInvalid
// — callers that surface the message into logs or operator-facing
// output get both pieces without re-parsing.
func TestValidateErrorWrapsSentinel(t *testing.T) {
	t.Parallel()

	err := netopts.Validate(netopts.TransportOptions{ReadDeadline: -42})
	require.Error(t, err)
	require.ErrorIs(t, err, netopts.ErrTransportOptionsInvalid)
	require.Contains(t, err.Error(), "ReadDeadline")
	require.Contains(t, err.Error(), "must not be negative")
}
