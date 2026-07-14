// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

type probeCtxKey struct{}

// TestRunCtxPropagatesRootContext pins the invariant that the CLI's
// root context reaches every subcommand through cmd.Context. Long-
// running subcommands (attach --auto-reconnect, list HOST, bind) depend
// on this propagation to observe SIGINT/SIGTERM via the cobra call
// chain; a main() that called cmd.Execute() without a context would
// silently seed a fresh context.Background and swallow the signal.
//
// The test injects a sentinel value into ctx, runs a probe subcommand
// whose RunE captures cmd.Context().Value through the same executeRoot
// dispatch used by runCtx, and verifies the sentinel survives unchanged.
func TestRunCtxPropagatesRootContext(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(context.Background(), probeCtxKey{}, "sentinel")

	var captured any

	root := newRootCmd()
	root.AddCommand(&cobra.Command{
		Use:    "__probe",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			captured = cmd.Context().Value(probeCtxKey{})

			return nil
		},
	})

	code, err := executeRoot(ctx, []string{"__probe"}, root)
	require.NoError(t, err, "probe subcommand should succeed")
	require.Equal(t, 0, code)
	require.Equal(t, "sentinel", captured,
		"runCtx must thread ctx through to every subcommand via cmd.Context")
}
