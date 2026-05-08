// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestExponentialBackoffConfigValidateRejectsOutOfRange pins the
// acceptance boundary for ExponentialBackoffConfig: Jitter must be in
// [0, 1); Min and Max must be non-negative with Max >= Min. Silently
// accepting an out-of-range value produces a schedule that either
// collapses to zero delay (under-escalated) or overshoots Max
// (over-escalated); neither is observable from the callsite until
// operators debug a reconnect storm.
func TestExponentialBackoffConfigValidateRejectsOutOfRange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cfg  usbip.ExponentialBackoffConfig
	}{
		{"jitter equals 1", usbip.ExponentialBackoffConfig{Min: time.Second, Max: time.Minute, Jitter: 1}},
		{"jitter above 1", usbip.ExponentialBackoffConfig{Min: time.Second, Max: time.Minute, Jitter: 1.5}},
		{"jitter negative", usbip.ExponentialBackoffConfig{Min: time.Second, Max: time.Minute, Jitter: -0.1}},
		{"min negative", usbip.ExponentialBackoffConfig{Min: -time.Second, Max: time.Minute}},
		{"max negative", usbip.ExponentialBackoffConfig{Min: time.Second, Max: -time.Minute}},
		{"max below min", usbip.ExponentialBackoffConfig{Min: time.Minute, Max: time.Second}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.cfg.Validate()
			require.Error(t, err, "expected %s to fail Validate", tc.name)
		})
	}
}

// TestExponentialBackoffConfigValidateAcceptsWellFormed keeps the
// happy path honest: a canonical config (1 s / 60 s / 0.2) passes.
func TestExponentialBackoffConfigValidateAcceptsWellFormed(t *testing.T) {
	t.Parallel()

	cfg := usbip.ExponentialBackoffConfig{
		Min:    time.Second,
		Max:    time.Minute,
		Jitter: 0.2,
	}

	require.NoError(t, cfg.Validate())
}
