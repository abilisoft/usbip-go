// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
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

func TestStatReportsMissingRequiresNotExist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "present path", err: nil, want: false},
		{name: "missing path", err: fs.ErrNotExist, want: true},
		{name: "wrapped missing path", err: fmt.Errorf("stat block: %w", fs.ErrNotExist), want: true},
		{name: "permission denied", err: fs.ErrPermission, want: false},
		{name: "invalid path", err: fs.ErrInvalid, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, statReportsMissing(test.err))
		})
	}
}

func TestVHCIStatusRowsAllNullFailsClosed(t *testing.T) {
	t.Parallel()

	const header = "hub port sta spd dev      sockfd local_busid\n"

	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "all high and superspeed rows are null",
			body: header +
				"hs  0000 004 000 00000000 000000 0-0\n" +
				"ss  0008 004 000 00000000 000000 0-0\n",
			want: true,
		},
		{name: "empty snapshot", body: "", want: false},
		{name: "header only", body: header, want: false},
		{
			name: "truncated row",
			body: header + "hs  0000 004\n",
			want: false,
		},
		{
			name: "unknown hub",
			body: header + "xx  0000 004 000 00000000 000000 0-0\n",
			want: false,
		},
		{
			name: "malformed numeric field",
			body: header + "hs  xxxx 004 000 00000000 000000 0-0\n",
			want: false,
		},
		{
			name: "not assigned remains claimed",
			body: header + "hs  0000 005 000 00000000 000000 0-0\n",
			want: false,
		},
		{
			name: "used row remains active",
			body: header + "hs  0000 006 003 00010002 000009 3-1\n",
			want: false,
		},
		{
			name: "null row with stale device identity",
			body: header + "hs  0000 004 000 00010002 000000 0-0\n",
			want: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, vhciStatusRowsAllNull(test.body))
		})
	}
}

func TestReleasedPortStatusExcludesClaimedStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status domain.Status
		want   bool
	}{
		{name: "null", status: domain.StatusNull, want: true},
		{name: "available", status: domain.StatusAvailable, want: true},
		{name: "not assigned", status: domain.StatusNotAssigned, want: false},
		{name: "used", status: domain.StatusUsed, want: false},
		{name: "error", status: domain.StatusError, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, test.want, releasedPortStatus(test.status))
		})
	}
}

func staticCommandOutput(out []byte) commandOutputFunc {
	return func(context.Context, string, ...string) ([]byte, error) {
		return out, nil
	}
}
