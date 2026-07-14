// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

var errChildBypassedHistoryLock = errors.New("child bypassed held history lock")

// TestHistoryReadAbsent — readHistory returns nil, no error, when the
// history file does not exist. I/O errors MUST be swallowed so shell
// completion never fails user-visibly.
func TestHistoryReadAbsent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	got := readHistory()
	require.Empty(t, got)
}

// TestHistoryReadExisting — readHistory returns each non-blank line as
// a separate entry, in file order.
func TestHistoryReadExisting(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, "usbip-go")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	body := []byte("10.0.0.5\n10.0.0.6\n\n192.168.1.1\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "history"), body, 0o600))

	got := readHistory()
	require.Equal(t, []string{testRemoteHost, "10.0.0.6", "192.168.1.1"}, got)
}

// TestReadHistoryFileRejectsOversizeLine covers scanner failure without
// weakening completion's public silent-on-error policy.
func TestReadHistoryFileRejectsOversizeLine(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), historyFileName)
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", 128*1024)), historyFileMode))

	_, err := readHistoryFile(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "scan history")
}

// TestHistoryRecordNew — a fresh recordHistory call creates the state
// dir (mode 0700) and writes the host as the first line.
func TestHistoryRecordNew(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	require.NoError(t, recordHistory(testRemoteHost))

	info, err := os.Stat(filepath.Join(tmp, "usbip-go"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())

	got := readHistory()
	require.Equal(t, []string{testRemoteHost}, got)
}

// TestHistoryRecordTightensExistingFileMode proves os.WriteFile's
// create-only mode semantics cannot leave a previously permissive history
// file world-readable. A successful rewrite must leave the inode at 0600.
func TestHistoryRecordTightensExistingFileMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	dir := filepath.Join(tmp, stateDirName)
	require.NoError(t, os.MkdirAll(dir, historyDirMode))

	path := filepath.Join(dir, historyFileName)

	writePermissiveHistoryFixture(t, path, []byte("legacy.example\n"))

	lockPath := path + ".lock"
	writePermissiveHistoryFixture(t, lockPath, nil)

	require.NoError(t, recordHistory(testRemoteHost))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, historyFileMode, info.Mode().Perm())
	require.Equal(t, []string{testRemoteHost, "legacy.example"}, readHistory())

	lockInfo, err := os.Stat(lockPath)
	require.NoError(t, err)
	require.Equal(t, historyFileMode, lockInfo.Mode().Perm())
}

// writePermissiveHistoryFixture deliberately creates a security-regression
// input broader than production permits and defeats a restrictive test umask.
func writePermissiveHistoryFixture(t *testing.T, path string, body []byte) {
	t.Helper()

	//nolint:gosec // Security regression requires an intentionally permissive fixture.
	require.NoError(t, os.WriteFile(path, body, 0o644))
	//nolint:gosec // Override a restrictive umask so the fixture is truly permissive.
	require.NoError(t, os.Chmod(path, 0o644))
}

// TestHistoryConcurrentRecordsPreserveEveryUpdate exercises the locked
// read-modify-write path with one simultaneous writer per retained slot.
// Correct locking makes the final order intentionally unspecified but cannot
// lose, duplicate, or partially serialize an entry.
func TestHistoryConcurrentRecordsPreserveEveryUpdate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	start := make(chan struct{})
	errs := make(chan error, historyCap)
	want := make([]string, 0, historyCap)

	var wg sync.WaitGroup

	for i := range historyCap {
		host := "host-" + itoaFast(i) + ".example"

		want = append(want, host)

		wg.Go(func() {
			<-start

			errs <- recordHistory(host)
		})
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	require.ElementsMatch(t, want, readHistory())
}

// TestHistoryIndependentProcessWaitsForLock proves the sidecar lock is a
// kernel-visible cross-process lock rather than merely an in-process mutex. The
// child reports that it is about to record, then must remain blocked while the
// parent owns the lock; after the parent publishes its update and unlocks, the
// child completes without losing either entry.
func TestHistoryIndependentProcessWaitsForLock(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stateDir := filepath.Join(dir, stateDirName)
	require.NoError(t, os.MkdirAll(stateDir, historyDirMode))

	path := filepath.Join(stateDir, historyFileName)
	childHost := "child.example"
	parentHost := "parent.example"

	var (
		child       *exec.Cmd
		childDone   chan error
		childStderr strings.Builder
	)

	err := withHistoryLock(path, func() error {
		//nolint:gosec // The command is the fixed current test binary with a fixed argument.
		child = exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestHistoryProcessHelper$")

		child.Env = append(
			os.Environ(),
			"USBIP_GO_HISTORY_HELPER=1",
			"USBIP_GO_HISTORY_PATH="+path,
			"USBIP_GO_HISTORY_HOST="+childHost,
		)
		child.Stderr = &childStderr

		stdout, pipeErr := child.StdoutPipe()
		if pipeErr != nil {
			return fmt.Errorf("open child stdout: %w", pipeErr)
		}

		startErr := child.Start()
		if startErr != nil {
			return fmt.Errorf("start child: %w", startErr)
		}

		childDone = make(chan error, 1)

		go func() {
			childDone <- child.Wait()
		}()

		scanner := bufio.NewScanner(stdout)
		if !scanner.Scan() || scanner.Text() != "ready" {
			return fmt.Errorf("await child ready: line=%q scan_err=%w", scanner.Text(), scanner.Err())
		}

		select {
		case waitErr := <-childDone:
			if waitErr != nil {
				return fmt.Errorf("%w: child wait: %w", errChildBypassedHistoryLock, waitErr)
			}

			return fmt.Errorf("%w: stderr=%s", errChildBypassedHistoryLock, childStderr.String())
		case <-time.After(100 * time.Millisecond):
		}

		return updateHistoryFile(path, parentHost)
	})
	require.NoError(t, err)

	select {
	case waitErr := <-childDone:
		require.NoError(t, waitErr, childStderr.String())
	case <-time.After(3 * time.Second):
		if child != nil && child.Process != nil {
			_ = child.Process.Kill()
		}

		t.Fatal("history helper did not exit after parent released lock")
	}

	entries, err := readHistoryFile(path)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{parentHost, childHost}, entries)
}

// TestHistoryProcessHelper is re-executed in a separate process by the lock
// regression above. The ready line is emitted immediately before recordHistory
// attempts to acquire the production sidecar flock.
func TestHistoryProcessHelper(t *testing.T) {
	if os.Getenv("USBIP_GO_HISTORY_HELPER") != "1" {
		t.Parallel()

		return
	}

	path := os.Getenv("USBIP_GO_HISTORY_PATH")
	host := os.Getenv("USBIP_GO_HISTORY_HOST")

	stateRoot := filepath.Dir(filepath.Dir(path))
	t.Setenv("XDG_STATE_HOME", stateRoot)
	t.Setenv("HOME", stateRoot)

	_, err := fmt.Fprintln(os.Stdout, "ready")
	require.NoError(t, err)
	require.NoError(t, recordHistory(host))
}

// TestHistoryRejectsSymlinkPath proves an update never follows a substituted
// history symlink and neither changes nor truncates its target.
func TestHistoryRejectsSymlinkPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)

	stateDir := filepath.Join(dir, stateDirName)
	require.NoError(t, os.MkdirAll(stateDir, historyDirMode))

	target := filepath.Join(dir, "target")
	want := []byte("private\n")
	require.NoError(t, os.WriteFile(target, want, historyFileMode))
	require.NoError(t, os.Symlink(target, filepath.Join(stateDir, historyFileName)))

	require.Error(t, recordHistory(testRemoteHost))

	//nolint:gosec // The target is a test-owned path inside t.TempDir.
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Empty(t, readHistory(), "completion must fail closed on a history symlink")
}

// TestHistoryRejectsSymlinkLock proves the lock open never follows a symlink
// and cannot chmod or write the linked target.
func TestHistoryRejectsSymlinkLock(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("HOME", dir)

	stateDir := filepath.Join(dir, stateDirName)
	require.NoError(t, os.MkdirAll(stateDir, historyDirMode))

	target := filepath.Join(dir, "lock-target")
	want := []byte("unchanged\n")
	writePermissiveHistoryFixture(t, target, want)
	require.NoError(t, os.Symlink(target, filepath.Join(stateDir, historyFileName)+".lock"))

	require.Error(t, recordHistory(testRemoteHost))

	//nolint:gosec // The target is a test-owned path inside t.TempDir.
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, want, got)

	info, err := os.Stat(target)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm())
}

// TestWithHistoryLockOperationErrors covers each fallible lock boundary and
// proves cleanup errors remain joined with operation results.
func TestWithHistoryLockOperationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*historyLockOps)
	}{
		{
			name: "open",
			mutate: func(ops *historyLockOps) {
				ops.open = func(string) (*os.File, error) { return nil, errTest }
			},
		},
		{
			name: "chmod",
			mutate: func(ops *historyLockOps) {
				ops.chmod = func(*os.File) error { return errTest }
			},
		},
		{
			name: "lock",
			mutate: func(ops *historyLockOps) {
				flock := ops.flock

				ops.flock = func(file *os.File, op int) error {
					if op == syscall.LOCK_EX {
						return errTest
					}

					return flock(file, op)
				}
			},
		},
		{
			name: "cleanup",
			mutate: func(ops *historyLockOps) {
				closeFile := ops.close

				ops.flock = func(*os.File, int) error { return errTest }
				ops.close = func(file *os.File) error {
					_ = closeFile(file)

					return errTest
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ops := defaultHistoryLockOps()
			tt.mutate(&ops)

			path := filepath.Join(t.TempDir(), historyFileName)
			err := withHistoryLockOps(path, func() error { return nil }, ops)
			require.ErrorIs(t, err, errTest)
		})
	}
}

// TestUpdateHistoryFileOperationErrors proves permission/read/write failures
// propagate from the locked history transaction with their classifications.
func TestUpdateHistoryFileOperationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*historyUpdateOps)
	}{
		{
			name: "open",
			mutate: func(ops *historyUpdateOps) {
				ops.open = func(string) (*os.File, error) { return nil, errTest }
			},
		},
		{
			name: "chmod",
			mutate: func(ops *historyUpdateOps) {
				ops.chmod = func(*os.File, os.FileMode) error { return errTest }
			},
		},
		{
			name: "read",
			mutate: func(ops *historyUpdateOps) {
				ops.read = func(*os.File) ([]string, error) { return nil, errTest }
			},
		},
		{
			name: "close",
			mutate: func(ops *historyUpdateOps) {
				closeFile := ops.close

				ops.close = func(file *os.File) error {
					_ = closeFile(file)

					return errTest
				}
			},
		},
		{
			name: "write",
			mutate: func(ops *historyUpdateOps) {
				ops.write = func(string, []byte) error { return errTest }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), historyFileName)
			require.NoError(t, os.WriteFile(path, nil, historyFileMode))

			ops := defaultHistoryUpdateOps()
			tt.mutate(&ops)

			err := updateHistoryFileOps(path, testRemoteHost, ops)
			require.ErrorIs(t, err, errTest)
		})
	}
}

// TestWriteHistoryAtomicOperationErrors covers every storage boundary and
// verifies failed generations leave no temporary file behind.
func TestWriteHistoryAtomicOperationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*historyAtomicOps)
	}{
		{
			name: "create temporary",
			mutate: func(ops *historyAtomicOps) {
				ops.createTemp = func(string, string) (*os.File, error) { return nil, errTest }
			},
		},
		{
			name: "close temporary",
			mutate: func(ops *historyAtomicOps) {
				closeFile := ops.close

				ops.close = func(file *os.File) error {
					_ = closeFile(file)

					return errTest
				}
			},
		},
		{
			name: "chmod temporary",
			mutate: func(ops *historyAtomicOps) {
				ops.chmod = func(*os.File, os.FileMode) error { return errTest }
			},
		},
		{
			name: "write temporary",
			mutate: func(ops *historyAtomicOps) {
				ops.write = func(*os.File, []byte) error { return errTest }
			},
		},
		{
			name: "rename temporary",
			mutate: func(ops *historyAtomicOps) {
				ops.rename = func(string, string) error { return errTest }
			},
		},
		{
			name: "remove temporary",
			mutate: func(ops *historyAtomicOps) {
				removeFile := ops.remove

				ops.write = func(*os.File, []byte) error { return errTest }
				ops.remove = func(path string) error {
					_ = removeFile(path)

					return errTest
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			ops := defaultHistoryAtomicOps()
			tt.mutate(&ops)

			err := writeHistoryAtomicOps(filepath.Join(dir, historyFileName), []byte("host\n"), ops)
			require.ErrorIs(t, err, errTest)

			entries, readErr := os.ReadDir(dir)
			require.NoError(t, readErr)
			require.Empty(t, entries, "failed atomic write must remove its temporary")
		})
	}
}

// TestWriteHistoryAtomicReadOnlyDescriptorReportsBodyWriteFailure exercises
// the production file.Write wrapper rather than replacing the write operation
// with a canned error. A descriptor opened read-only must preserve EBADF
// through both layers of history-write context, then be removed during cleanup.
func TestWriteHistoryAtomicReadOnlyDescriptorReportsBodyWriteFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "read-only-temporary")
	require.NoError(t, os.WriteFile(tmpPath, nil, historyFileMode))

	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })

	ops := defaultHistoryAtomicOps()

	ops.createTemp = func(string, string) (*os.File, error) {
		return root.Open("read-only-temporary")
	}

	err = writeHistoryAtomicOps(filepath.Join(dir, historyFileName), []byte("host\n"), ops)
	require.ErrorIs(t, err, syscall.EBADF)
	require.ErrorContains(t, err, "write history body")
	require.ErrorContains(t, err, "write history temporary")
	require.NoFileExists(t, tmpPath)
}

// TestWriteHistoryAtomicMissingTemporaryCleanupPreservesPrimaryError proves an
// already-absent retained temporary is the desired cleanup state. The primary
// write failure remains visible, while the cleanup ENOENT is not joined into
// the result.
func TestWriteHistoryAtomicMissingTemporaryCleanupPreservesPrimaryError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ops := defaultHistoryAtomicOps()

	ops.write = func(file *os.File, _ []byte) error {
		err := os.Remove(file.Name())
		if err != nil {
			return fmt.Errorf("remove retained history temporary: %w", err)
		}

		return errTest
	}

	err := writeHistoryAtomicOps(filepath.Join(dir, historyFileName), []byte("host\n"), ops)
	require.ErrorIs(t, err, errTest)
	require.NotErrorIs(t, err, fs.ErrNotExist,
		"an absent cleanup target must not add ENOENT to the primary failure")
}

// TestHistoryRecordDedup — recording an existing host promotes it to
// the most-recent slot without introducing duplicates.
func TestHistoryRecordDedup(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	require.NoError(t, recordHistory(testRemoteHost))
	require.NoError(t, recordHistory("10.0.0.6"))
	require.NoError(t, recordHistory(testRemoteHost))

	// Most-recent-first: 10.0.0.5 moved to front, 10.0.0.6 follows, no dup.
	require.Equal(t, []string{testRemoteHost, "10.0.0.6"}, readHistory())
}

// TestHistoryRecordCap — the history file is capped at 20 entries.
// The 21st insert evicts the oldest.
func TestHistoryRecordCap(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	for i := range historyCap + 5 {
		host := "10.0.0." + itoaFast(i)
		require.NoError(t, recordHistory(host))
	}

	got := readHistory()
	require.Len(t, got, historyCap)

	// Most-recent-first: last insert is the newest host at index 0.
	require.Equal(t, "10.0.0.24", got[0])
}

// itoaFast is a tiny decimal formatter kept local to the test to avoid
// importing strconv just for this fixture.
func itoaFast(n int) string {
	if n == 0 {
		return "0"
	}

	out := ""

	for n > 0 {
		out = string(rune('0'+n%10)) + out

		n /= 10
	}

	return out
}

// TestAttachFirstArgCompletionUsesHistory — the attach ValidArgsFunction
// returns history entries when completing the first positional arg.
func TestAttachFirstArgCompletionUsesHistory(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmp)
	t.Setenv("HOME", tmp)

	require.NoError(t, recordHistory(testRemoteHost))
	require.NoError(t, recordHistory("server.local"))

	swapFactories(t, &mockImporter{}, &mockExporter{})

	cmd := newAttachCmd()
	got, _ := cmd.ValidArgsFunction(cmd, nil, "")

	// Most-recent first.
	require.Equal(t, []string{"server.local", testRemoteHost}, got)
}

// TestAttachSecondArgCompletionDisabledByDefault — when neither
// USBIP_COMPLETE_NETWORK=1 nor --complete-network is set, the second
// arg returns an empty completion list and does NOT dial.
func TestAttachSecondArgCompletionDisabledByDefault(t *testing.T) {
	t.Setenv("USBIP_COMPLETE_NETWORK", "")

	var dialed bool

	imp := &mockImporter{
		listRemoteFn: func(_ context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			dialed = true

			return nil, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newAttachCmd()
	got, _ := cmd.ValidArgsFunction(cmd, []string{testRemoteHost}, "")

	require.Empty(t, got)
	require.False(t, dialed, "network completion must not dial when disabled")
}

// TestAttachSecondArgCompletionEnabledByEnv — with USBIP_COMPLETE_NETWORK=1
// the completion dials ListRemote and returns the busid list.
func TestAttachSecondArgCompletionEnabledByEnv(t *testing.T) {
	t.Setenv("USBIP_COMPLETE_NETWORK", "1")

	imp := &mockImporter{
		listRemoteFn: func(ctx context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			// The completion MUST cap the dial at 800ms via context
			// deadline; assert the deadline exists.
			dl, ok := ctx.Deadline()
			require.True(t, ok, "ListRemote context must carry a deadline")
			require.LessOrEqual(t, time.Until(dl), 800*time.Millisecond)

			return []usbip.Device{
				{BusID: testNestedBusID, VendorID: 0xabcd, ProductID: 0x0001},
				{BusID: testSecondaryBusID, VendorID: 0x1111, ProductID: 0x2222},
			}, nil
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newAttachCmd()
	got, _ := cmd.ValidArgsFunction(cmd, []string{testRemoteHost}, "")

	require.Len(t, got, 2)
	require.Contains(t, got[0], testNestedBusID)
	require.Contains(t, got[1], testSecondaryBusID)
}

// TestAttachSecondArgCompletionSilentOnError — ListRemote failure
// returns empty + no error (cli-interface OpenSpec silent-on-failure rule).
func TestAttachSecondArgCompletionSilentOnError(t *testing.T) {
	t.Setenv("USBIP_COMPLETE_NETWORK", "1")

	imp := &mockImporter{
		listRemoteFn: func(_ context.Context, _ usbip.RemoteEndpoint) ([]usbip.Device, error) {
			return nil, errTest
		},
	}
	swapFactories(t, imp, &mockExporter{})

	cmd := newAttachCmd()
	got, _ := cmd.ValidArgsFunction(cmd, []string{testRemoteHost}, "")
	require.Empty(t, got)
}
