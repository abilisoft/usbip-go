// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestHistoryReadAbsent — readHistory returns nil, no error, when the
// history file does not exist. I/O errors MUST be swallowed so shell
// completion never fails user-visibly.
func TestHistoryReadAbsent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	got := readHistory()
	require.Empty(t, got)
}

// TestHistoryReadExisting — readHistory returns each non-blank line as
// a separate entry, in file order.
func TestHistoryReadExisting(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, "usbip")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	body := []byte("10.0.0.5\n10.0.0.6\n\n192.168.1.1\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "history"), body, 0o600))

	got := readHistory()
	require.Equal(t, []string{"10.0.0.5", "10.0.0.6", "192.168.1.1"}, got)
}

// TestHistoryRecordNew — a fresh recordHistory call creates the state
// dir (mode 0700) and writes the host as the first line.
func TestHistoryRecordNew(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	require.NoError(t, recordHistory("10.0.0.5"))

	info, err := os.Stat(filepath.Join(tmp, "usbip"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	got := readHistory()
	require.Equal(t, []string{"10.0.0.5"}, got)
}

// TestHistoryRecordDedup — recording an existing host promotes it to
// the most-recent slot without introducing duplicates.
func TestHistoryRecordDedup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	require.NoError(t, recordHistory("10.0.0.5"))
	require.NoError(t, recordHistory("10.0.0.6"))
	require.NoError(t, recordHistory("10.0.0.5"))

	// Most-recent-first: 10.0.0.5 moved to front, 10.0.0.6 follows, no dup.
	require.Equal(t, []string{"10.0.0.5", "10.0.0.6"}, readHistory())
}

// TestHistoryRecordCap — the history file is capped at 20 entries.
// The 21st insert evicts the oldest.
func TestHistoryRecordCap(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	for i := range historyCap + 5 {
		host := "10.0.0." + itoaFast(i)
		require.NoError(t, recordHistory(host))
	}

	got := readHistory()
	require.Len(t, got, historyCap)

	// Most-recent-first: last insert is the newest host at index 0.
	require.Equal(t, "10.0.0.24", got[0])
}

// itoaFast is a tiny decimal formatter kept local to the test to avoid
// importing strconv just for this fixture.
func itoaFast(n int) string {
	if n == 0 {
		return "0"
	}

	out := ""

	for n > 0 {
		out = string(rune('0'+n%10)) + out

		n /= 10
	}

	return out
}

// TestAttachFirstArgCompletionUsesHistory — the attach ValidArgsFunction
// returns history entries when completing the first positional arg.
func TestAttachFirstArgCompletionUsesHistory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	require.NoError(t, recordHistory("10.0.0.5"))
	require.NoError(t, recordHistory("server.local"))

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newAttachCmd()
	got, _ := cmd.ValidArgsFunction(cmd, nil, "")

	// Most-recent first.
	require.Equal(t, []string{"server.local", "10.0.0.5"}, got)
}

// TestAttachSecondArgCompletionDisabledByDefault — when neither
// USBIP_COMPLETE_NETWORK=1 nor --complete-network is set, the second
// arg returns an empty completion list and does NOT dial.
func TestAttachSecondArgCompletionDisabledByDefault(t *testing.T) {
	t.Setenv("USBIP_COMPLETE_NETWORK", "")

	var dialed bool

	imp := &mockImporter{
		listRemoteFn: func(_ context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			dialed = true

			return nil, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newAttachCmd()
	got, _ := cmd.ValidArgsFunction(cmd, []string{"10.0.0.5"}, "")

	require.Empty(t, got)
	require.False(t, dialed, "network completion must not dial when disabled")
}

// TestAttachSecondArgCompletionEnabledByEnv — with USBIP_COMPLETE_NETWORK=1
// the completion dials ListRemote and returns the busid list.
func TestAttachSecondArgCompletionEnabledByEnv(t *testing.T) {
	t.Setenv("USBIP_COMPLETE_NETWORK", "1")

	imp := &mockImporter{
		listRemoteFn: func(ctx context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			// The completion MUST cap the dial at 800ms via context
			// deadline; assert the deadline exists.
			dl, ok := ctx.Deadline()
			require.True(t, ok, "ListRemote context must carry a deadline")
			require.LessOrEqual(t, time.Until(dl), 800*time.Millisecond)

			return []usbip.Device{
				{BusID: "1-1.2", VendorID: 0xabcd, ProductID: 0x0001},
				{BusID: "2-1", VendorID: 0x1111, ProductID: 0x2222},
			}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newAttachCmd()
	got, _ := cmd.ValidArgsFunction(cmd, []string{"10.0.0.5"}, "")

	require.Len(t, got, 2)
	require.Contains(t, got[0], "1-1.2")
	require.Contains(t, got[1], "2-1")
}

// TestAttachSecondArgCompletionSilentOnError — ListRemote failure
// returns empty + no error (v1 contract §7.6 silent-on-failure rule).
func TestAttachSecondArgCompletionSilentOnError(t *testing.T) {
	t.Setenv("USBIP_COMPLETE_NETWORK", "1")

	imp := &mockImporter{
		listRemoteFn: func(_ context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			return nil, errTest
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newAttachCmd()
	got, _ := cmd.ValidArgsFunction(cmd, []string{"10.0.0.5"}, "")
	require.Empty(t, got)
}
