// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package transport_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/abilisoft/usbip-go/internal/adapter/transport"
	"github.com/abilisoft/usbip-go/internal/netopts"
)

// TestListenCtxAlreadyCancelledDistinguishable pins the invariant
// that a Listen call against a pre-cancelled ctx surfaces a
// distinguishable error prefix vs. a Listen failure mid-bind. Both
// paths currently wrap the same "listen <addr>: %w" template, so an
// operator grepping `journalctl -u usbipd-go` cannot tell whether the
// socket was even attempted. The contract here does NOT constrain
// the exact wording beyond "the two paths must not be identical".
func TestListenCtxAlreadyCancelledDistinguishable(t *testing.T) {
	t.Parallel()

	tr := transport.New(transport.WithLogger(slog.Default()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := tr.Listen(ctx, "127.0.0.1:0", netopts.TransportOptions{})
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled,
		"pre-cancelled ctx must surface context.Canceled up the chain")
	require.Contains(t, err.Error(), "before bind",
		"pre-check branch must identify itself so operators can grep")
}
