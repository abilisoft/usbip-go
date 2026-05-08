// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/pkg/domain"
	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// TestRenderAttachResult_Table pins the attach table-mode output:
// "✓ attached <busid>" line ahead of the port table. Without a
// dedicated test, a regression that drops the formatAck prefix
// (or stops calling styleWriter on the ack writer) would only
// surface in a manual run.
func TestRenderAttachResult_Table(t *testing.T) {
	t.Parallel()

	cmd := &cobra.Command{}

	var out bytes.Buffer

	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetContext(withOutputCtx(context.Background(), outputTable))

	port := usbip.Port{
		ID:    7,
		BusID: domain.BusID("3-1"),
	}

	require.NoError(t, renderAttachResult(cmd, port))

	got := out.String()
	require.Contains(t, got, "attached",
		"table-mode attach must show the styled ack")
	require.Contains(t, got, "3-1",
		"ack must name the busid the operator just attached")
	require.True(t, strings.Contains(got, "PORT") || strings.Contains(got, "port"),
		"attach table must include the port detail row after the ack")
}

// TestWriteBindAck_TableBindAndUnbind pins both bind+unbind
// table-mode acks: "✓ bound <busid>" and "✓ unbinded <busid>"
// styled through formatAck. Existing bind tests only exercised
// --output=json so the styled path was uncovered.
func TestWriteBindAck_TableBindAndUnbind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		op    string
		want  string
		busID domain.BusID
	}{
		{op: "bind", want: "binded", busID: "1-2"},
		{op: "unbind", want: "unbinded", busID: "3-1"},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			t.Parallel()

			cmd := &cobra.Command{}

			var out bytes.Buffer

			cmd.SetOut(&out)
			cmd.SetErr(&out)
			cmd.SetContext(withOutputCtx(context.Background(), outputTable))

			require.NoError(t, writeBindAck(cmd, tc.op, tc.busID))

			got := out.String()
			require.Contains(t, got, tc.want,
				"table-mode %s ack must contain action verb %q", tc.op, tc.want)
			require.Contains(t, got, string(tc.busID),
				"table-mode %s ack must contain busid", tc.op)
		})
	}
}

// withOutputCtx seeds the output-format ctx-key the renderer reads
// via outputFromCtx; mirrors the production PersistentPreRunE
// wiring without dragging in the full root command.
func withOutputCtx(parent context.Context, format string) context.Context {
	return context.WithValue(parent, flagsCtxKey, &globalFlags{Output: format})
}
