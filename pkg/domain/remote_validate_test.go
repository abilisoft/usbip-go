// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestRemoteEndpointValidateRejectsEmptyHost pins the invariant:
// RemoteEndpoint with an empty Host must not silently normalize to
// ":3240" and hand itself to a dialer that resolves the empty host to
// localhost. The struct is a public value type so the zero value is
// reachable via literal construction; Validate is the checked
// boundary every service entry point must call before dialing.
func TestRemoteEndpointValidateRejectsEmptyHost(t *testing.T) {
	t.Parallel()

	err := (domain.RemoteEndpoint{}).Validate()
	require.Error(t, err, "empty Host must fail Validate")
}

// TestRemoteEndpointValidateAcceptsWellFormed confirms the happy
// path: a host satisfying the library's parsing rules passes Validate
// regardless of whether Port was set (the zero-Port default-port
// convention is orthogonal to host validation).
func TestRemoteEndpointValidateAcceptsWellFormed(t *testing.T) {
	t.Parallel()

	cases := []domain.RemoteEndpoint{
		{Host: "peer.example"},
		{Host: "192.0.2.1", Port: 3240},
		{Host: "::1"},
		{Host: "peer.example", Port: 4000},
	}

	for _, r := range cases {
		err := r.Validate()
		require.NoError(t, err, "endpoint %+v should pass Validate", r)
	}
}
