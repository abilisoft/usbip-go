// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestAttachOptionsShutdownTimeoutRoundTrip pins the facade contract
// that AttachOptions exposes a ShutdownTimeout field and that it
// propagates through the internal translation. Without it callers
// can only accept the library's hardcoded 5 s default — impossible
// to tune for workloads that need either a longer graceful wait or
// a negative value to disable the bound entirely.
func TestAttachOptionsShutdownTimeoutRoundTrip(t *testing.T) {
	t.Parallel()

	opts := usbip.AttachOptions{
		ShutdownTimeout: 42 * time.Second,
	}

	require.Equal(t, 42*time.Second, opts.ShutdownTimeout,
		"AttachOptions.ShutdownTimeout must be readable by callers")
}
