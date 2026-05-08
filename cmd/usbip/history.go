// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// historyCap bounds the number of entries the XDG-state-backed attach
// history retains. v1 contract §7.6 locks this to 20 so long-running shells
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
// state root — matches the package module so multiple CLIs can share
// the same $XDG_STATE_HOME without colliding.
const stateDirName = "usbip"

// readHistory returns the attach-history entries most-recent-first.
// I/O errors (missing file, permission denied, malformed UTF-8) are
// swallowed and return an empty slice — shell completion must never
// surface an error to the user. os.ReadFile is preferred over os.Open
// here because the caller never seeks or streams and ReadFile's single
// call gives us a small, audit-friendly surface for the state-file
// path (gosec G304).
func readHistory() []string {
	path, err := historyPath()
	if err != nil {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var out []string

	s := bufio.NewScanner(bytes.NewReader(data))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}

		out = append(out, line)
	}

	if s.Err() != nil {
		return nil
	}

	return out
}

// recordHistory appends host to the history file, dedupes existing
// entries so host moves to the most-recent slot, and truncates the
// list to historyCap entries. The file is rewritten atomically-ish via
// a create-truncate write. Returns an error so callers can surface
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

	entries := readHistory()

	entries = slices.DeleteFunc(entries, func(s string) bool { return s == host })
	entries = append([]string{host}, entries...)

	if len(entries) > historyCap {
		entries = entries[:historyCap]
	}

	body := strings.Join(entries, "\n")
	if body != "" {
		body += "\n"
	}

	err = os.WriteFile(path, []byte(body), historyFileMode)
	if err != nil {
		return fmt.Errorf("write history: %w", err)
	}

	return nil
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
