// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// historyCap bounds the number of entries the XDG-state-backed attach
// history retains. cli-interface OpenSpec locks this to 20 so long-running shells
// do not amass an unbounded suggestion list.
const historyCap = 20

// historyDirMode is the mode applied to the state dir when we create
// it on the first recordHistory call. 0700 is tight because the file
// may accidentally leak remote hostnames a user considered private.
const historyDirMode os.FileMode = 0o700

// historyFileMode is the mode applied to the history file itself.
const historyFileMode os.FileMode = 0o600

// historyFileName is the basename of the history file inside the state
// dir. Kept as a constant so tests can reference it without a second
// source of truth.
const historyFileName = "history"

// stateDirName is the per-binary subdirectory name under the XDG
// state root — matches the binary so multiple USB/IP-related CLIs
// can share the same $XDG_STATE_HOME without colliding (notably
// upstream `usbip` from linux-tools).
const stateDirName = "usbip-go"

// readHistory returns the attach-history entries most-recent-first.
// I/O errors (missing file, permission denied, malformed UTF-8) are
// swallowed and return an empty slice — shell completion must never
// surface an error to the user. The final history component is opened with
// O_NOFOLLOW so completion cannot be redirected through a substituted symlink.
func readHistory() []string {
	path, err := historyPath()
	if err != nil {
		return nil
	}

	entries, err := readHistoryFile(path)
	if err != nil {
		return nil
	}

	return entries
}

// readHistoryFile parses one concrete history path. recordHistory calls this
// while holding the cross-process lock so its read-modify-write transaction
// cannot lose an update; completion calls it through readHistory and preserves
// the existing silent-on-error policy.
func readHistoryFile(path string) ([]string, error) {
	file, err := openHistoryNoFollow(filepath.Clean(path), os.O_RDONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("open history: %w", err)
	}

	entries, readErr := scanHistory(file)
	closeErr := file.Close()

	return entries, errors.Join(readErr, wrapHistoryCleanupError("close history", closeErr))
}

// openHistoryNoFollow rejects a history or lock symlink at the final path
// component. Both files contain user-private state, and following a replaced
// pathname would let an update chmod or overwrite an unrelated target.
func openHistoryNoFollow(path string, flag int, perm os.FileMode) (*os.File, error) {
	file, err := os.OpenFile(path, flag|unix.O_NOFOLLOW, perm)
	if err != nil {
		return nil, fmt.Errorf("open no-follow history path: %w", err)
	}

	return file, nil
}

// scanHistory parses history from an already-open descriptor. Keeping the
// descriptor open across validation and reading prevents pathname substitution
// between permission tightening and the read-modify-write transaction.
func scanHistory(file *os.File) ([]string, error) {
	var out []string

	s := bufio.NewScanner(file)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}

		out = append(out, line)
	}

	if s.Err() != nil {
		return nil, fmt.Errorf("scan history: %w", s.Err())
	}

	return out, nil
}

// recordHistory appends host to the history file, dedupes existing
// entries so host moves to the most-recent slot, and truncates the
// list to historyCap entries. The complete read-modify-write transaction is
// serialized with a sidecar flock and the file is replaced atomically. Returns
// an error so callers can surface
// disk failures in logs; completion callers are expected to ignore
// the return value because user-visible silence matters more than
// perfect write bookkeeping.
func recordHistory(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}

	path, err := historyPath()
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(path), historyDirMode)
	if err != nil {
		return fmt.Errorf("mkdir history dir: %w", err)
	}

	return withHistoryLock(path, func() error {
		return updateHistoryFile(path, host)
	})
}

// withHistoryLock serializes the complete history read-modify-write sequence
// across concurrent usbip-go processes. flock is released automatically if a
// process crashes, avoiding the stale-lock failure mode of O_EXCL lock files.
func withHistoryLock(path string, update func() error) error {
	return withHistoryLockOps(path, update, defaultHistoryLockOps())
}

// historyLockOps is the narrow syscall seam for deterministic lock-cleanup
// error coverage. Production constructs it locally; tests alter their own copy,
// so no package-global mutation or cross-test race is possible.
type historyLockOps struct {
	open  func(string) (*os.File, error)
	chmod func(*os.File) error
	flock func(*os.File, int) error
	close func(*os.File) error
}

func defaultHistoryLockOps() historyLockOps {
	return historyLockOps{
		open: func(path string) (*os.File, error) {
			return openHistoryNoFollow(path, os.O_CREATE|os.O_RDWR, historyFileMode)
		},
		chmod: func(file *os.File) error {
			return file.Chmod(historyFileMode)
		},
		flock: func(file *os.File, op int) error {
			return syscall.Flock(int(file.Fd()), op)
		},
		close: func(file *os.File) error {
			return file.Close()
		},
	}
}

func withHistoryLockOps(
	path string,
	update func() error,
	ops historyLockOps,
) error {
	lockPath := filepath.Clean(path + ".lock")

	lockFile, err := ops.open(lockPath)
	if err != nil {
		return fmt.Errorf("open history lock: %w", err)
	}

	err = ops.chmod(lockFile)
	if err != nil {
		return finishHistoryLock(lockFile, ops, fmt.Errorf("chmod history lock: %w", err))
	}

	err = ops.flock(lockFile, syscall.LOCK_EX)
	if err != nil {
		return finishHistoryLock(lockFile, ops, fmt.Errorf("lock history: %w", err))
	}

	return finishHistoryLock(lockFile, ops, update())
}

// finishHistoryLock preserves the primary operation result while always
// reporting unlock and close failures before returning ownership.
func finishHistoryLock(lockFile *os.File, ops historyLockOps, result error) error {
	unlockErr := ops.flock(lockFile, syscall.LOCK_UN)
	closeErr := ops.close(lockFile)

	return errors.Join(result,
		wrapHistoryCleanupError("unlock history", unlockErr),
		wrapHistoryCleanupError("close history lock", closeErr))
}

// wrapHistoryCleanupError preserves nil for errors.Join while adding useful
// operation context to a failed unlock or close.
func wrapHistoryCleanupError(op string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", op, err)
}

// updateHistoryFile opens an existing inode without following a final symlink,
// tightens and reads that same descriptor, updates the bounded MRU list, and
// atomically replaces the pathname with a 0600 temporary.
func updateHistoryFile(path, host string) error {
	return updateHistoryFileOps(path, host, defaultHistoryUpdateOps())
}

// historyUpdateOps isolates the fallible storage operations around the bounded
// MRU transform without mutable package-level test seams.
type historyUpdateOps struct {
	open  func(string) (*os.File, error)
	chmod func(*os.File, os.FileMode) error
	read  func(*os.File) ([]string, error)
	close func(*os.File) error
	write func(string, []byte) error
}

func defaultHistoryUpdateOps() historyUpdateOps {
	return historyUpdateOps{
		open: func(path string) (*os.File, error) {
			return openHistoryNoFollow(path, os.O_RDONLY, 0)
		},
		chmod: func(file *os.File, mode os.FileMode) error {
			return file.Chmod(mode)
		},
		read: scanHistory,
		close: func(file *os.File) error {
			return file.Close()
		},
		write: writeHistoryAtomic,
	}
}

func updateHistoryFileOps(path, host string, ops historyUpdateOps) error {
	file, openErr := ops.open(filepath.Clean(path))

	entries, err := readHistoryForUpdate(file, openErr, ops)
	if err != nil {
		return err
	}

	entries = slices.DeleteFunc(entries, func(s string) bool { return s == host })
	entries = append([]string{host}, entries...)

	if len(entries) > historyCap {
		entries = entries[:historyCap]
	}

	body := strings.Join(entries, "\n")
	if body != "" {
		body += "\n"
	}

	return ops.write(path, []byte(body))
}

// readHistoryForUpdate handles the optional existing generation while keeping
// its chmod, read, and close on one descriptor.
func readHistoryForUpdate(
	file *os.File,
	openErr error,
	ops historyUpdateOps,
) ([]string, error) {
	switch {
	case errors.Is(openErr, fs.ErrNotExist):
		return nil, nil
	case openErr != nil:
		return nil, fmt.Errorf("open history: %w", openErr)
	}

	chmodErr := ops.chmod(file, historyFileMode)
	if chmodErr != nil {
		return nil, errors.Join(
			fmt.Errorf("chmod history: %w", chmodErr),
			wrapHistoryCleanupError("close history", ops.close(file)),
		)
	}

	entries, readErr := ops.read(file)

	closeErr := ops.close(file)
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(
			readErr,
			wrapHistoryCleanupError("close history", closeErr),
		)
	}

	return entries, nil
}

// writeHistoryAtomic writes a private temporary in the history directory and
// renames it over the destination. Readers therefore observe either the old
// complete file or the new complete file, never a truncated intermediate.
func writeHistoryAtomic(path string, body []byte) error {
	return writeHistoryAtomicOps(path, body, defaultHistoryAtomicOps())
}

// historyAtomicOps provides deterministic error injection for each storage
// boundary while production retains the standard-library implementation.
type historyAtomicOps struct {
	createTemp func(string, string) (*os.File, error)
	chmod      func(*os.File, os.FileMode) error
	write      func(*os.File, []byte) error
	close      func(*os.File) error
	rename     func(string, string) error
	remove     func(string) error
}

func defaultHistoryAtomicOps() historyAtomicOps {
	return historyAtomicOps{
		createTemp: os.CreateTemp,
		chmod: func(file *os.File, mode os.FileMode) error {
			return file.Chmod(mode)
		},
		write: func(file *os.File, body []byte) error {
			_, err := file.Write(body)
			if err != nil {
				return fmt.Errorf("write history body: %w", err)
			}

			return nil
		},
		close: func(file *os.File) error {
			return file.Close()
		},
		rename: os.Rename,
		remove: os.Remove,
	}
}

func writeHistoryAtomicOps(path string, body []byte, ops historyAtomicOps) error {
	tmp, err := ops.createTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		return fmt.Errorf("create history temporary: %w", err)
	}

	tmpPath := tmp.Name()

	err = ops.chmod(tmp, historyFileMode)
	if err != nil {
		return finishHistoryTemporary(
			tmp,
			tmpPath,
			ops,
			fmt.Errorf("chmod history temporary: %w", err),
		)
	}

	err = ops.write(tmp, body)
	if err != nil {
		return finishHistoryTemporary(
			tmp,
			tmpPath,
			ops,
			fmt.Errorf("write history temporary: %w", err),
		)
	}

	closeErr := ops.close(tmp)
	if closeErr != nil {
		return removeHistoryTemporary(
			tmpPath,
			ops,
			fmt.Errorf("close history temporary: %w", closeErr),
		)
	}

	err = ops.rename(filepath.Clean(tmpPath), filepath.Clean(path))
	if err != nil {
		return removeHistoryTemporary(
			tmpPath,
			ops,
			fmt.Errorf("replace history: %w", err),
		)
	}

	return nil
}

// finishHistoryTemporary closes an unsuccessful generation and joins any
// cleanup failures with the primary operation error.
func finishHistoryTemporary(
	tmp *os.File,
	tmpPath string,
	ops historyAtomicOps,
	result error,
) error {
	closeErr := ops.close(tmp)

	return removeHistoryTemporary(
		tmpPath,
		ops,
		errors.Join(result, wrapHistoryCleanupError("close history temporary", closeErr)),
	)
}

// removeHistoryTemporary preserves cleanup failures instead of silently
// discarding them. A missing path is already the desired postcondition.
func removeHistoryTemporary(tmpPath string, ops historyAtomicOps, result error) error {
	removeErr := ops.remove(filepath.Clean(tmpPath))
	if errors.Is(removeErr, fs.ErrNotExist) {
		removeErr = nil
	}

	return errors.Join(result, wrapHistoryCleanupError("remove history temporary", removeErr))
}

// historyPath resolves the absolute path to the history file, honouring
// $XDG_STATE_HOME first, then $HOME/.local/state per the XDG Base
// Directory spec. A host without a readable $HOME is a hard failure
// here; readHistory swallows it silently.
func historyPath() (string, error) {
	root, err := stateHome()
	if err != nil {
		return "", err
	}

	return filepath.Join(root, stateDirName, historyFileName), nil
}

// stateHome returns $XDG_STATE_HOME when set, else $HOME/.local/state.
func stateHome() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return v, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}

	return filepath.Join(home, ".local", "state"), nil
}
