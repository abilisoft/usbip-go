// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestWatchEmitsJSONLines — the mocked Watch yields three events;
// `usbip-go watch --output=json` emits three jsonlines each with
// "schema":"v1" + "kind":"...".
func TestWatchEmitsJSONLines(t *testing.T) {
	t.Parallel()

	events := []usbip.Event{
		domain.PortAttachedEvent{
			At:   time.Unix(100, 0).UTC(),
			Port: usbip.Port{ID: 1, BusID: "1-1.2"},
		},
		domain.PortErroredEvent{
			At:   time.Unix(101, 0).UTC(),
			Port: usbip.Port{ID: 1, BusID: "1-1.2"},
			Err:  "boom",
		},
		domain.PortDetachedEvent{
			At:     time.Unix(102, 0).UTC(),
			Port:   usbip.Port{ID: 1, BusID: "1-1.2"},
			Reason: "user",
		},
	}

	imp := &mockImporter{
		watchFn: func(_ context.Context) iter.Seq[usbip.Event] {
			return func(yield func(usbip.Event) bool) {
				for _, e := range events {
					if !yield(e) {
						return
					}
				}
			}
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "watch"})

	err := cmd.Execute()
	require.NoError(t, err)

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	require.Len(t, lines, 3)

	kinds := make([]string, 0, len(lines))

	for _, l := range lines {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &m))
		require.Equal(t, "v1", m["schema"])
		require.Contains(t, m, "kind")

		kind, _ := m["kind"].(string)

		kinds = append(kinds, kind)
	}

	require.Equal(t, []string{"port_attached", "port_errored", "port_detached"}, kinds)
}

// TestWatchStopsOnContextCancel — cancelling the context during
// iteration ends the loop without error. We arrange the mock to cancel
// between yields and assert execute returns nil.
func TestWatchStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		watchFn: func(ctx context.Context) iter.Seq[usbip.Event] {
			return func(yield func(usbip.Event) bool) {
				// Yield one event, then respect a cancelled ctx.
				if !yield(domain.PortAttachedEvent{
					At:   time.Unix(1, 0).UTC(),
					Port: usbip.Port{ID: 0},
				}) {
					return
				}

				<-ctx.Done()
			}
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"--output=json", "watch"})

	ctx, cancel := context.WithCancel(context.Background())
	cmd.SetContext(ctx)

	// Cancel shortly after the command starts so the watcher exits.
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	err := cmd.Execute()
	require.NoError(t, err)
}

// TestWatchTableFormat — default table output still produces one line
// per event with the kind + timestamp visible.
func TestWatchTableFormat(t *testing.T) {
	t.Parallel()

	imp := &mockImporter{
		watchFn: func(_ context.Context) iter.Seq[usbip.Event] {
			return func(yield func(usbip.Event) bool) {
				_ = yield(domain.DeviceBoundEvent{
					At:     time.Unix(42, 0).UTC(),
					Device: sampleDevice(),
				})
			}
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newRootCmd()

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"watch"})

	err := cmd.Execute()
	require.NoError(t, err)
	require.Contains(t, out.String(), "device_bound")
}
