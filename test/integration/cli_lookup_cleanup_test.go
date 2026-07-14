// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseAttachPortID_ReturnsAcknowledgedPort(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(map[string]any{
		"schema": "v1",
		"op":     "attach",
		"ok":     true,
		"port": map[string]any{
			"id":    uint32(0),
			"busid": "1-1",
		},
	})
	require.NoError(t, err)

	require.Equal(t, uint32(0), parseAttachPortID(t, out, "1-1"))
}

// TestWaitForAttachedPortByID_AcceptsDifferentLocalBusID is the regression
// for same-host loopback. The exporter device is 1-1, but vhci_hcd enumerates
// it under the importer-local topology as 2-1. The acknowledged port id, not
// either busid field, is the stable identity across those views.
func TestWaitForAttachedPortByID_AcceptsDifferentLocalBusID(t *testing.T) {
	t.Parallel()

	out, err := json.Marshal(map[string]any{
		"schema": "v1",
		"ports": []map[string]any{
			{
				"id":          uint32(0),
				"status":      "used",
				"busid":       "1-1",
				"local_busid": "2-1",
			},
		},
	})
	require.NoError(t, err)

	err = waitForAttachedPortByID(
		context.Background(),
		staticCommandOutput(out),
		"usbip-go",
		0,
		100*time.Millisecond,
		time.Millisecond,
	)
	require.NoError(t, err)
}

// TestWaitForAttachedPortByID_RetriesUntilKernelConverges reproduces the
// ordering where Attach returns its reserved port before `port --id` can see
// the kernel's StatusUsed row. A transient lookup failure must be retried.
func TestWaitForAttachedPortByID_RetriesUntilKernelConverges(t *testing.T) {
	t.Parallel()

	assigned, err := json.Marshal(map[string]any{
		"schema": "v1",
		"ports": []map[string]any{
			{"id": uint32(3), "status": "used", "local_busid": "2-1"},
		},
	})
	require.NoError(t, err)

	calls := 0
	output := func(context.Context, string, ...string) ([]byte, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("port is not attached yet")
		}

		return assigned, nil
	}

	err = waitForAttachedPortByID(
		context.Background(),
		output,
		"usbip-go",
		3,
		100*time.Millisecond,
		time.Millisecond,
	)
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func staticCommandOutput(out []byte) commandOutputFunc {
	return func(context.Context, string, ...string) ([]byte, error) {
		return out, nil
	}
}
