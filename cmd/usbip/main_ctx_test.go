package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestRunCtxPropagatesCancellation pins the invariant that the CLI's
// root context reaches every subcommand. A cancelled ctx passed to
// runCtx must surface through cmd.Context() inside the subcommand so
// long-running subcommands (attach --auto-reconnect, list -r, bind)
// can observe SIGINT/SIGTERM and exit cleanly instead of blocking in
// a kernel or network call.
func TestRunCtxPropagatesCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// "list -l" reads cmd.Context() and calls pkg/usbip which honours
	// context cancellation on the local adapter path. A pre-cancelled
	// ctx must surface as a non-nil error from runCtx (not an OS exit
	// code of 0 meaning "success") via the listed subcommand's
	// ctx-aware read path.
	code, err := runCtx(ctx, []string{"list", "-l"})
	require.NotEqual(t, 0, code,
		"cancelled ctx must surface a non-zero exit code from runCtx")
	require.Error(t, err,
		"cancelled ctx must surface an error from runCtx")
}
