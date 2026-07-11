// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package domain_test

import (
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/stretchr/testify/require"
)

func TestConstants_KnownValues(t *testing.T) {
	t.Parallel()

	require.Equal(t, uint16(3240), domain.DefaultPort)
	require.Equal(t, "0.0.0.0:3240", domain.DefaultEndpoint)
	require.Equal(t, uint16(0x0111), domain.ProtocolVersion)
	require.Equal(t, 32, domain.BusIDSize)
	require.Equal(t, 256, domain.SysPathSize)
	require.Equal(t, 10*time.Second, domain.DefaultDialTimeout)
	require.Equal(t, 5*time.Second, domain.DefaultHandshakeTimeout)
	require.Equal(t, 10*time.Second, domain.DefaultExporterHandshakeTimeout)
	require.Equal(t, 30*time.Second, domain.DefaultShutdownTimeout)
}
