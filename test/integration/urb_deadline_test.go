// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlockDeviceDeadlineForMode(t *testing.T) {
	t.Parallel()

	require.Equal(t, blockDevDeadlineKVM, blockDeviceDeadlineForMode(""))
	require.Equal(t, blockDevDeadlineKVM, blockDeviceDeadlineForMode("0"))
	require.Equal(t, blockDevDeadlineTCG, blockDeviceDeadlineForMode(kernelVMTGCEnvEnabled))
}
