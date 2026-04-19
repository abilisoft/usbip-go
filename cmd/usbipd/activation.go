package main

import (
	"context"
	"fmt"
	"net"

	"github.com/coreos/go-systemd/v22/activation"
)

// activationFdName is the expected LISTEN_FDNAMES label for our socket
// unit. Operators should set FileDescriptorName=usbipd on the .socket
// unit (spec §7.8); mismatches fail loudly rather than silently bind a
// neighbouring socket.
const activationFdName = "usbipd"

// listenOrActivation returns the listener usbipd should Serve on. It
// prefers systemd-passed named sockets, falls back to an unnamed single
// fd, and finally falls back to a plain net.Listen on cfg.Listen.
//
// The policy matches spec §7.7:
//   - If LISTEN_FDNAMES contains "usbipd" with exactly one fd, use it.
//   - If LISTEN_FDS=1 and no names are present, accept the single fd.
//   - If multiple fds are passed without a matching name, refuse to
//     guess and return an error.
//   - Otherwise, plain net.Listen on cfg.Listen.
func listenOrActivation(cfg *Config) (net.Listener, error) {
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

	return netListenCtx(context.Background(), cfg.Listen)
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

	total := 0
	for _, ls := range named {
		total += len(ls)
	}

	switch {
	case total == 1:
		for _, ls := range named {
			if len(ls) == 1 {
				return ls[0], true, nil
			}
		}
	case total > 1:
		// Multiple fds but none labelled "usbipd" — refuse to guess.
		// Close every listener so fds don't leak before we return.
		for _, ls := range named {
			for _, l := range ls {
				_ = l.Close()
			}
		}

		return nil, false, fmt.Errorf(
			"LISTEN_FDS passed but no socket named %q and multiple fds present",
			activationFdName)
	}

	return nil, false, nil
}

// netListenCtx is a tiny wrapper that accepts the future net.ListenConfig
// context-aware listen call. Today it delegates to net.Listen for v1
// compatibility with the cmd/usbip client path; wrapping the call keeps
// Phase 9's context-aware upgrade a one-line change.
func netListenCtx(_ context.Context, addr string) (net.Listener, error) {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %q: %w", addr, err)
	}

	return lis, nil
}
