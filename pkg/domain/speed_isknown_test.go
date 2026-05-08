// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// TestSpeedIsKnownMatrix pins the finite enum of USB speed values.
// Callers that receive a domain.Speed off the wire need a predicate
// for "did the peer send a value inside the kernel's enum_device_speed
// range?"; the stdlib enum emits unknown values as "speed(N)" instead
// of erring, which hides garbage from downstream consumers.
func TestSpeedIsKnownMatrix(t *testing.T) {
	t.Parallel()

	known := []domain.Speed{
		domain.SpeedUnknown,
		domain.SpeedLow,
		domain.SpeedFull,
		domain.SpeedHigh,
		domain.SpeedWireless,
		domain.SpeedSuper,
		domain.SpeedSuperPlus,
	}

	for _, s := range known {
		require.True(t, s.IsKnown(), "Speed(%d) should be known", s)
	}

	unknown := []domain.Speed{
		7,
		99,
		0xDEADBEEF,
	}

	for _, s := range unknown {
		require.False(t, s.IsKnown(), "Speed(%d) must not be known", s)
	}
}
