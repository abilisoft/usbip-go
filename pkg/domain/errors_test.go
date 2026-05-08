// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestSentinels_AreDistinctAndNonNil(t *testing.T) {
	t.Parallel()

	sentinels := []struct {
		name string
		err  error
		msg  string
	}{
		{"ErrDeviceNotFound", domain.ErrDeviceNotFound, "device not found"},
		{"ErrDeviceAlreadyBound", domain.ErrDeviceAlreadyBound, "device already bound"},
		{"ErrDeviceNotBound", domain.ErrDeviceNotBound, "device not bound"},
		{"ErrPortInUse", domain.ErrPortInUse, "port in use"},
		{"ErrNoFreePort", domain.ErrNoFreePort, "no free vhci port"},
		{"ErrProtocolMismatch", domain.ErrProtocolMismatch, "usbip protocol version mismatch"},
		{"ErrProtocolError", domain.ErrProtocolError, "usbip protocol error reported by peer"},
		{"ErrAlreadyRunning", domain.ErrAlreadyRunning, "another instance is already running"},
		{"ErrAlreadyShutdown", domain.ErrAlreadyShutdown, "exporter already shut down"},
		{"ErrBusIDInvalid", domain.ErrBusIDInvalid, "invalid bus id"},
		{"ErrPermission", domain.ErrPermission, "operation requires elevated privileges"},
		{"ErrKernelModuleMissing", domain.ErrKernelModuleMissing, "required kernel module not loaded"},
	}

	seen := make(map[error]string, len(sentinels))
	for _, s := range sentinels {
		require.Errorf(t, s.err, "%s is nil", s.name)
		require.Equalf(t, s.msg, s.err.Error(), "%s message", s.name)

		prev, dup := seen[s.err]
		require.Falsef(t, dup, "%s duplicates %s", s.name, prev)

		seen[s.err] = s.name
	}
}

func TestSentinels_PairwiseDistinct(t *testing.T) {
	t.Parallel()

	// Sanity: each sentinel is not Is of another.
	require.NotErrorIs(t, domain.ErrDeviceNotFound, domain.ErrPortInUse)
	require.NotErrorIs(t, domain.ErrProtocolError, domain.ErrProtocolMismatch)
	require.NotErrorIs(t, domain.ErrAlreadyRunning, domain.ErrAlreadyShutdown)
}
