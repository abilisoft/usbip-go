// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"

	"github.com/coreos/go-systemd/v22/activation"
)

// activationFdName is the expected LISTEN_FDNAMES label for our socket
// unit. Operators should set FileDescriptorName=usbip-go on the .socket
// unit (v1 contract §7.8); mismatches fail loudly rather than silently
// bind a neighbouring socket.
const activationFdName = "usbip-go"

// errAmbiguousSocketNames is returned by pickNamedListener when systemd
// passes more than one socket but none carry the expected "usbip-go"
// FileDescriptorName. The operator must fix their .socket unit; we
// refuse to guess.
var errAmbiguousSocketNames = errors.New(
	"LISTEN_FDS passed but no matching FileDescriptorName and multiple fds present",
)

// listenersWithNames is the seam for activation.ListenersWithNames; swapped
// in tests to inject errors without manipulating process-level fds.
var listenersWithNames = activation.ListenersWithNames

// listenOrActivation returns the listener usbip-go serve should Serve on.
// It prefers systemd-passed named sockets, falls back to an unnamed
// single fd, and finally falls back to a plain net.Listen on cfg.Listen.
//
// The policy matches v1 contract §7.7:
//   - If LISTEN_FDNAMES contains "usbip-go" with exactly one fd, use it.
//   - If LISTEN_FDS=1 and no names are present, accept the single fd.
//   - If multiple fds are passed without a matching name, refuse to
//     guess and return an error.
//   - Otherwise, plain net.ListenConfig on cfg.Listen.
func listenOrActivation(ctx context.Context, cfg *ServeConfig) (net.Listener, error) {
	named, err := listenersWithNames()
	if err != nil {
		// ListenersWithNames returns a non-nil error when the
		// process was not started via systemd socket activation
		// (LISTEN_FDS / LISTEN_PID unset). That is the normal
		// hand-run case; falling back to plain Listen is correct.
		// Surface the reason at debug so an operator who DID expect
		// activation can spot the misconfiguration in the log.
		loggerOrDefault(ctx).Debug("systemd socket activation unavailable; falling back to --listen",
			"err", err)
	} else if len(named) > 0 {
		lis, activated, perr := pickNamedListener(ctx, named)
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
//
// When a single unnamed fd is accepted whose label does not match
// activationFdName, a Warn is emitted naming both the observed and
// expected labels. Operators upgrading from the pre-rename
// `FileDescriptorName=usbip` socket unit see the warning and can
// realign their unit without observing a silent fallback.
func pickNamedListener(ctx context.Context, named map[string][]net.Listener) (net.Listener, bool, error) {
	fds, ok := named[activationFdName]
	if ok && len(fds) == 1 {
		return fds[0], true, nil
	}

	total := countListeners(named)

	if total == 1 {
		observed := firstSingletonListenerName(named)
		if observed != activationFdName {
			loggerOrDefault(ctx).Warn("systemd activation fd label mismatch; "+
				"accepting it as singleton fallback — update the socket "+
				"unit's FileDescriptorName to silence this warning",
				slog.String("observed", observed),
				slog.String("expected", activationFdName))
		}

		return firstSingletonListener(named), true, nil
	}

	if total > 1 {
		closeAllListeners(named)

		return nil, false, fmt.Errorf("%w: want %q", errAmbiguousSocketNames, activationFdName)
	}

	return nil, false, nil
}

// firstSingletonListenerName returns the label of the named map's
// only entry. Precondition: total == 1 (verified by the caller via
// countListeners). Returns the empty string when the map has no
// entries — the caller treats that as the same "fall through" path
// the listener-less branch already covers.
func firstSingletonListenerName(named map[string][]net.Listener) string {
	for name, ls := range named {
		if len(ls) == 1 {
			return name
		}
	}

	return ""
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
