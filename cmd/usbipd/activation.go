// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/coreos/go-systemd/v22/activation"
)

// activationFdName is the expected LISTEN_FDNAMES label for our socket
// unit. Operators should set FileDescriptorName=usbipd on the .socket
// unit (spec §7.8); mismatches fail loudly rather than silently bind a
// neighbouring socket.
const activationFdName = "usbipd"

// errAmbiguousSocketNames is returned by pickNamedListener when systemd
// passes more than one socket but none carry the expected "usbipd"
// FileDescriptorName. The operator must fix their .socket unit; we
// refuse to guess.
var errAmbiguousSocketNames = errors.New(
	"LISTEN_FDS passed but no matching FileDescriptorName and multiple fds present")

// listenOrActivation returns the listener usbipd should Serve on. It
// prefers systemd-passed named sockets, falls back to an unnamed single
// fd, and finally falls back to a plain net.Listen on cfg.Listen.
//
// The policy matches spec §7.7:
//   - If LISTEN_FDNAMES contains "usbipd" with exactly one fd, use it.
//   - If LISTEN_FDS=1 and no names are present, accept the single fd.
//   - If multiple fds are passed without a matching name, refuse to
//     guess and return an error.
//   - Otherwise, plain net.ListenConfig on cfg.Listen.
func listenOrActivation(ctx context.Context, cfg *Config) (net.Listener, error) {
	named, err := activation.ListenersWithNames()
	if err == nil && len(named) > 0 {
		lis, activated, perr := pickNamedListener(named)
		if perr != nil {
			return nil, perr
		}

		if activated {
			return lis, nil
		}
	}

	return netListenCtx(ctx, cfg.Listen)
}

// pickNamedListener implements the §7.7 decision table. The boolean
// return disambiguates the "no listener, no error, caller should fall
// back" state from the "error" state — nil-nil returns are a known
// trip hazard (nilnil lint) so the tri-state is explicit instead.
func pickNamedListener(named map[string][]net.Listener) (net.Listener, bool, error) {
	fds, ok := named[activationFdName]
	if ok && len(fds) == 1 {
		return fds[0], true, nil
	}

	total := countListeners(named)

	if total == 1 {
		return firstSingletonListener(named), true, nil
	}

	if total > 1 {
		closeAllListeners(named)

		return nil, false, fmt.Errorf("%w: want %q", errAmbiguousSocketNames, activationFdName)
	}

	return nil, false, nil
}

// countListeners sums the total number of listeners across every named
// slot. Extracted so pickNamedListener keeps its cognitive complexity
// under the gocognit threshold.
func countListeners(named map[string][]net.Listener) int {
	total := 0
	for _, ls := range named {
		total += len(ls)
	}

	return total
}

// firstSingletonListener returns the one-and-only listener when the
// named map has a single fd under any key. Precondition: callers have
// verified total == 1 via countListeners.
func firstSingletonListener(named map[string][]net.Listener) net.Listener {
	for _, ls := range named {
		if len(ls) == 1 {
			return ls[0]
		}
	}

	return nil
}

// closeAllListeners releases every fd referenced by named so an
// ambiguous-socket rejection does not leak kernel resources.
func closeAllListeners(named map[string][]net.Listener) {
	for _, ls := range named {
		for _, l := range ls {
			_ = l.Close()
		}
	}
}

// netListenCtx wraps net.ListenConfig.Listen so the accept-path listener
// honours ctx cancellation during bind. Delegating through a named
// helper keeps the production call site and tests symmetric.
func netListenCtx(ctx context.Context, addr string) (net.Listener, error) {
	var lc net.ListenConfig

	lis, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %q: %w", addr, err)
	}

	return lis, nil
}
