// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// dup3FdOnto performs the syscall.Dup3(srcFd, target, 0) with the
// uintptr→int narrowing guarded inline so gosec G115's flow analysis
// sees a direct overflow comparison (same pattern as
// cmd/usbip.isStderrTTY's os.Stderr.Fd() guard). External syscall
// errors are wrapped with fmt.Errorf to satisfy wrapcheck.
func dup3FdOnto(t *testing.T, srcFd uintptr, target int) error {
	t.Helper()

	if srcFd > uintptr(^uint(0)>>1) { // exceeds max int on this platform
		t.Fatalf("file descriptor %d exceeds platform int range", srcFd)

		return nil
	}

	err := syscall.Dup3(int(srcFd), target, 0)
	if err != nil {
		return fmt.Errorf("dup3 %d -> %d: %w", srcFd, target, err)
	}

	return nil
}

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

// listenerFile extracts the backing *os.File from a *net.TCPListener.
// File() dup's the underlying socket; the returned file and the
// original listener can be closed independently.
func listenerFile(t *testing.T, lis *net.TCPListener) *os.File {
	t.Helper()

	f, err := lis.File()
	require.NoError(t, err)

	return f
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

	cfg := &Config{Listen: "127.0.0.1:0"}

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
	t.Setenv("LISTEN_FDNAMES", "usbipd")

	cfg := &Config{Listen: "127.0.0.1:0"}

	lis, err := listenOrActivation(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, lis)

	t.Cleanup(func() { _ = lis.Close() })
}

// TestListenOrActivationNamedSocket drives the positive activation
// path: a pre-bound listener whose fd is dup'd to SD_LISTEN_FDS_START
// (fd 3), with LISTEN_* env vars set to match. We use a subprocess-free
// dup: the current process's fd 3 is temporarily replaced, then restored
// in t.Cleanup so parallel tests in the same binary don't observe the
// mutation (but this test is non-parallel for safety).
//
// Note: tests that manipulate fd 3 cannot run in parallel — the dup is
// per-process, not per-goroutine.
func TestListenOrActivationNamedSocket(t *testing.T) {
	// Intentionally NOT t.Parallel(): fd 3 is shared process state.
	srcLis := tcpListen(t, "127.0.0.1:0")
	t.Cleanup(func() { _ = srcLis.Close() })

	// Extract the raw fd from the *net.TCPListener. File() dup's the
	// underlying socket so we can freely dup it again to fd 3 without
	// disturbing the original listener.
	srcFile := listenerFile(t, srcLis)
	t.Cleanup(func() { _ = srcFile.Close() })

	// Preserve whatever fd 3 currently points at (if anything) so we
	// can restore it after the test.
	origFd3 := preserveFd(t, 3)

	// Dup the source fd onto fd 3. After this, fd 3 refers to the same
	// socket as srcLis, and activation.Files will hand it to us.
	require.NoError(t, dup3FdOnto(t, srcFile.Fd(), 3))

	t.Cleanup(func() { restoreFd(t, 3, origFd3) })

	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LISTEN_FDS", "1")
	t.Setenv("LISTEN_FDNAMES", "usbipd")

	cfg := &Config{Listen: "127.0.0.1:1"} // unused

	lis, err := listenOrActivation(context.Background(), cfg)
	require.NoError(t, err)
	require.NotNil(t, lis)

	t.Cleanup(func() { _ = lis.Close() })

	// Prove the returned listener is the one that was passed in via
	// fd 3: both listeners share the same local address.
	require.Equal(t, srcLis.Addr().String(), lis.Addr().String())
}

// TestListenOrActivationAmbiguousFds covers the §7.7 "no socket named
// 'usbipd' and multiple fds present" error branch. Two listeners are
// dup'd to fd 3 and fd 4 with non-matching names.
func TestListenOrActivationAmbiguousFds(t *testing.T) {
	// Non-parallel: mutates fd 3 and fd 4.
	lisA := tcpListen(t, "127.0.0.1:0")
	t.Cleanup(func() { _ = lisA.Close() })

	lisB := tcpListen(t, "127.0.0.1:0")
	t.Cleanup(func() { _ = lisB.Close() })

	fileA := listenerFile(t, lisA)
	t.Cleanup(func() { _ = fileA.Close() })

	fileB := listenerFile(t, lisB)
	t.Cleanup(func() { _ = fileB.Close() })

	origFd3 := preserveFd(t, 3)
	origFd4 := preserveFd(t, 4)

	require.NoError(t, dup3FdOnto(t, fileA.Fd(), 3))
	t.Cleanup(func() { restoreFd(t, 3, origFd3) })

	require.NoError(t, dup3FdOnto(t, fileB.Fd(), 4))
	t.Cleanup(func() { restoreFd(t, 4, origFd4) })

	t.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	t.Setenv("LISTEN_FDS", "2")
	// Non-matching names so the library returns a name map keyed on
	// "other0" / "other1" — both different from "usbipd".
	t.Setenv("LISTEN_FDNAMES", "other0:other1")

	cfg := &Config{Listen: "127.0.0.1:1"}

	lis, err := listenOrActivation(context.Background(), cfg)
	require.Error(t, err)
	require.Nil(t, lis)
	require.Contains(t, err.Error(), "usbipd")
}

// preserveFd returns the fd used to save whatever fd `target` currently
// references. -1 means "fd was closed / not present"; callers pass that
// sentinel back to restoreFd for a close.
func preserveFd(t *testing.T, target int) int {
	t.Helper()

	saved, err := syscall.Dup(target)
	if err != nil {
		// Non-existent fd: treat as -1 (nothing to restore).
		return -1
	}

	return saved
}

// restoreFd reverses preserveFd — if saved is non-negative, dup it back
// to target and close the saved fd; else close target (since we created
// it during the test).
func restoreFd(t *testing.T, target, saved int) {
	t.Helper()

	if saved < 0 {
		_ = syscall.Close(target)

		return
	}

	err := syscall.Dup3(saved, target, 0)
	if err != nil {
		t.Logf("restoreFd: dup3 failed: %v", err)
	}

	_ = syscall.Close(saved)
}
