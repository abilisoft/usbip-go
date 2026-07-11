// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

var errActivationInjected = errors.New("injected activation error")

// tcpListen spins a TCP listener bound to loopback using the context-
// aware ListenConfig path; noctx demands no bare net.Listen.
func tcpListen(t *testing.T, addr string) *net.TCPListener {
	t.Helper()

	var lc net.ListenConfig

	lis, err := lc.Listen(context.Background(), "tcp", addr)
	require.NoError(t, err)

	tcpLis, ok := lis.(*net.TCPListener)
	require.True(t, ok, "expected *net.TCPListener, got %T", lis)

	return tcpLis
}

// TestListenOrActivationFallsBackWhenNoEnv verifies that without any
// LISTEN_* env vars, listenOrActivation falls back to a plain TCP
// listen on cfg.Listen.
func TestListenOrActivationFallsBackWhenNoEnv(t *testing.T) {
	// t.Setenv incompatible with t.Parallel — env is process state.

	// Setenv with empty values to ensure inheritance from parent doesn't
	// accidentally trigger the activation path.
	t.Setenv("LISTEN_PID", "")
	t.Setenv("LISTEN_FDS", "")
	t.Setenv("LISTEN_FDNAMES", "")

	cfg := &ServeConfig{Listen: testEphemeralListenAddr}

	lis, err := listenOrActivation(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, lis)

	t.Cleanup(func() { _ = lis.Close() })

	// Sanity — the listener must be a TCP listener on IPv4 loopback.
	tcpAddr, ok := lis.Addr().(*net.TCPAddr)
	require.True(t, ok, "expected *net.TCPAddr, got %T", lis.Addr())
	require.True(t, tcpAddr.IP.IsLoopback())
}

// TestListenOrActivationIgnoresMismatchedPID verifies the activation
// library's LISTEN_PID check — a PID pointing elsewhere must not be
// consumed. listenOrActivation must fall back to plain listen.
func TestListenOrActivationIgnoresMismatchedPID(t *testing.T) {
	// t.Setenv incompatible with t.Parallel.

	// PID 1 is init; our tests never run as PID 1.
	t.Setenv("LISTEN_PID", "1")
	t.Setenv("LISTEN_FDS", "1")
	t.Setenv("LISTEN_FDNAMES", "usbip-go")

	cfg := &ServeConfig{Listen: testEphemeralListenAddr}

	lis, err := listenOrActivation(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, lis)

	t.Cleanup(func() { _ = lis.Close() })
}

// TestListenOrActivationNamedSocket drives the positive activation
// path via the listenersWithNames seam. Earlier versions of this test
// dup'd sockets onto fd 3/4 to mimic systemd directly, but those low
// fds may be owned by the Go runtime netpoller in-process; swapping
// them makes unrelated networking tests crash with EBADF.
func TestListenOrActivationNamedSocket(t *testing.T) {
	// Not parallel: swaps the package-level seam.
	orig := listenersWithNames

	srcLis := tcpListen(t, testEphemeralListenAddr)
	t.Cleanup(func() { _ = srcLis.Close() })

	listenersWithNames = func() (map[string][]net.Listener, error) {
		return map[string][]net.Listener{activationFdName: {srcLis}}, nil
	}

	t.Cleanup(func() { listenersWithNames = orig })

	cfg := &ServeConfig{Listen: testActivatedListenAddr} // unused when activation succeeds

	lis, err := listenOrActivation(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, lis)
	require.Same(t, srcLis, lis)
}

// TestListenOrActivationAmbiguousFds covers the §7.7 "no socket named
// 'usbip-go' and multiple fds present" error branch. It injects the
// activation map rather than dup'ing low process fds, because fd 3/4
// may belong to the Go runtime or surrounding test harness.
func TestListenOrActivationAmbiguousFds(t *testing.T) {
	// Not parallel: swaps the package-level seam.
	orig := listenersWithNames

	lisA := tcpListen(t, testEphemeralListenAddr)
	t.Cleanup(func() { _ = lisA.Close() })

	lisB := tcpListen(t, testEphemeralListenAddr)
	t.Cleanup(func() { _ = lisB.Close() })

	listenersWithNames = func() (map[string][]net.Listener, error) {
		return map[string][]net.Listener{
			"other0": {lisA},
			"other1": {lisB},
		}, nil
	}

	t.Cleanup(func() { listenersWithNames = orig })

	cfg := &ServeConfig{Listen: testActivatedListenAddr} // unused when ambiguity is detected

	lis, err := listenOrActivation(context.Background(), cfg)
	require.Error(t, err)
	require.Nil(t, lis)
	require.ErrorIs(t, err, errAmbiguousSocketNames)
	require.Contains(t, err.Error(), "usbip-go")
}

// TestListenOrActivation_LegacyFdNameWarns covers the
// pickNamedListener branch where exactly one fd is supplied under a
// non-matching FileDescriptorName (the typical "operator upgraded the
// binary but kept their old socket unit's `FileDescriptorName=usbip`"
// scenario). The fd MUST be accepted as the singleton fallback so a
// rename does not yank socket activation out from under deployed
// systems, but a Warn must fire so the unit can be realigned.
func TestListenOrActivation_LegacyFdNameWarns(t *testing.T) {
	// Not parallel: swaps the package-level seam.
	orig := listenersWithNames

	var lc net.ListenConfig

	srcLis, err := lc.Listen(context.Background(), "tcp", testEphemeralListenAddr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = srcLis.Close() })

	legacyName := "usbip"

	listenersWithNames = func() (map[string][]net.Listener, error) {
		return map[string][]net.Listener{legacyName: {srcLis}}, nil
	}

	t.Cleanup(func() { listenersWithNames = orig })

	cfg := &ServeConfig{Listen: testActivatedListenAddr}

	lis, err := listenOrActivation(context.Background(), cfg)
	require.NoError(t, err,
		"legacy-name singleton must be accepted, not rejected")
	require.Same(t, srcLis, lis,
		"the very listener supplied under the legacy name must be returned")
}

// TestPickNamedListener_NoFds covers the
// "no fds at all" branch in pickNamedListener so the fall-through to
// plain net.Listen on cfg.Listen is exercised even without injecting
// the listenersWithNames seam.
func TestPickNamedListener_NoFds(t *testing.T) {
	t.Parallel()

	lis, activated, err := pickNamedListener(t.Context(), map[string][]net.Listener{})
	require.NoError(t, err)
	require.False(t, activated)
	require.Nil(t, lis)
}

// TestFirstSingletonListenerName_EmptyMap pins the post-condition:
// when the named map has no entries, the helper returns the empty
// string so callers treat it as the "no observed name" sentinel.
func TestFirstSingletonListenerName_EmptyMap(t *testing.T) {
	t.Parallel()

	require.Empty(t, firstSingletonListenerName(map[string][]net.Listener{}))
}

// TestListenOrActivation_ErrorPath covers the err != nil branch in
// listenOrActivation: when listenersWithNames returns an error the function
// must log at debug and fall back to plain listen rather than propagating the
// error. The seam is swapped so no fd manipulation is required.
func TestListenOrActivation_ErrorPath(t *testing.T) {
	// Not parallel: swaps the package-level seam.
	orig := listenersWithNames

	listenersWithNames = func() (map[string][]net.Listener, error) {
		return nil, errActivationInjected
	}

	t.Cleanup(func() { listenersWithNames = orig })

	cfg := &ServeConfig{Listen: testEphemeralListenAddr}

	lis, err := listenOrActivation(context.Background(), cfg)
	require.NoError(t, err,
		"activation error must not propagate; listenOrActivation must fall back to plain listen")
	require.NotNil(t, lis)

	t.Cleanup(func() { _ = lis.Close() })
}
