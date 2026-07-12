// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLookupPortIDByBusIDForCleanup_HandlesMissingBinary pins that
// the cleanup helper does NOT panic / fatal when the supplied
// binary cannot run; the cleanup contract is "best effort, never
// abort the test from a t.Cleanup goroutine". A panicking cleanup
// would fail the parent test even when the test body succeeded.
func TestLookupPortIDByBusIDForCleanup_HandlesMissingBinary(t *testing.T) {
	t.Parallel()

	id, err := lookupPortIDByBusIDForCleanup(
		context.Background(),
		commandOutput,
		"/path/that/does/not/exist",
		"1-1",
	)
	require.Empty(t, id)
	require.Error(t, err,
		"missing binary must surface an error so the caller skips the detach attempt")
}

// TestLookupPortIDByBusIDForCleanup_HandlesNonJSONOutput pins
// fatal-free behavior when the listed binary returns non-JSON
// output (an obvious flag misuse, an old binary, etc.). The
// cleanup must return ("", error) instead of panicking on the
// json.Unmarshal call.
func TestLookupPortIDByBusIDForCleanup_HandlesNonJSONOutput(t *testing.T) {
	t.Parallel()

	id, err := lookupPortIDByBusIDForCleanup(
		context.Background(),
		staticCommandOutput([]byte("not-json")),
		"usbip-go",
		"1-1",
	)
	require.Empty(t, id)
	require.Error(t, err,
		"non-JSON output from port must surface as an unmarshal error so the cleanup short-circuits")
}

// TestLookupPortIDByBusIDForCleanup_ReturnsEmptyOnNoMatch pins that
// a valid envelope with no matching busid returns ("", nil) — not
// an error. The cleanup interprets that as "nothing to detach,
// move on".
func TestLookupPortIDByBusIDForCleanup_ReturnsEmptyOnNoMatch(t *testing.T) {
	t.Parallel()

	envelope := map[string]any{
		"schema": "v1",
		"ports": []map[string]any{
			{"id": float64(0), "local_busid": "9-9"},
		},
	}

	out, err := json.Marshal(envelope)
	require.NoError(t, err)

	id, lookupErr := lookupPortIDByBusIDForCleanup(
		context.Background(),
		staticCommandOutput(out),
		"usbip-go",
		"1-1",
	)
	require.NoError(t, lookupErr,
		"a clean envelope with no matching busid is not an error")
	require.Empty(t, id,
		"no match returns the empty string; cleanup interprets that as no-op")
}

// TestLookupPortIDByBusIDForCleanup_FindsByBusID pins the happy
// path: when one port row matches the requested busid, the
// helper returns its id.
func TestLookupPortIDByBusIDForCleanup_FindsByBusID(t *testing.T) {
	t.Parallel()

	envelope := map[string]any{
		"schema": "v1",
		"ports": []map[string]any{
			{"id": float64(0), "local_busid": "9-9"},
			{"id": float64(2), "local_busid": "1-1"},
		},
	}

	out, err := json.Marshal(envelope)
	require.NoError(t, err)

	id, lookupErr := lookupPortIDByBusIDForCleanup(
		context.Background(),
		staticCommandOutput(out),
		"usbip-go",
		"1-1",
	)
	require.NoError(t, lookupErr)
	require.Equal(t, "2", id,
		"port row matching the requested busid must be returned (not ports[0])")
}

// TestWaitForPortIDByBusID_RetriesUntilKernelConverges reproduces the hosted
// TCG ordering where Attach returns before vhci_hcd publishes the assigned
// bus ID. The first status snapshot is unassigned; the second contains the
// imported device and must succeed without treating the first snapshot as a
// terminal failure.
func TestWaitForPortIDByBusID_RetriesUntilKernelConverges(t *testing.T) {
	t.Parallel()

	unassigned, err := json.Marshal(map[string]any{
		"schema": "v1",
		"ports":  []map[string]any{{"id": float64(0), "local_busid": "0-0"}},
	})
	require.NoError(t, err)

	assigned, err := json.Marshal(map[string]any{
		"schema": "v1",
		"ports":  []map[string]any{{"id": float64(0), "local_busid": "1-1"}},
	})
	require.NoError(t, err)

	calls := 0
	output := func(context.Context, string, ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return unassigned, nil
		}

		return assigned, nil
	}

	id, waitErr := waitForPortIDByBusID(
		context.Background(),
		output,
		"usbip-go",
		"1-1",
		100*time.Millisecond,
		time.Millisecond,
	)
	require.NoError(t, waitErr)
	require.Equal(t, "0", id)
	require.Equal(t, 2, calls)
}

func staticCommandOutput(out []byte) commandOutputFunc {
	return func(context.Context, string, ...string) ([]byte, error) {
		return out, nil
	}
}
