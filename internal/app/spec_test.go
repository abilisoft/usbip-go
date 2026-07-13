// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"testing"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

// TestRemoteDeviceSpecFields asserts every field on a RemoteDeviceSpec
// literal is reachable and carries the value the constructor passed in,
// including the pre-handoff local-port reservation hook.
func TestRemoteDeviceSpecFields(t *testing.T) {
	t.Parallel()

	dev := domain.Device{
		Path:  "/sys/x",
		BusID: domain.BusID("1-2"),
	}
	remote := domain.RemoteEndpoint{Host: "h", Port: 3240}
	reserved := make(chan domain.PortID, 1)

	spec := app.RemoteDeviceSpec{
		Device: dev,
		DevID:  domain.DeviceID(0x10002),
		Speed:  domain.SpeedHigh,
		Remote: remote,
		ReserveLocalPort: func(id domain.PortID) error {
			reserved <- id

			return nil
		},
	}

	require.Equal(t, dev, spec.Device)
	require.Equal(t, domain.DeviceID(0x10002), spec.DevID)
	require.Equal(t, domain.SpeedHigh, spec.Speed)
	require.Equal(t, remote, spec.Remote)
	require.NoError(t, spec.ReserveLocalPort(domain.PortID(7)))
	require.Equal(t, domain.PortID(7), <-reserved)
}
