// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package usbip

import (
	"io/fs"
)

// probeOneAtForTestInvoke is the Linux-tagged shim used by
// ProbeOneAtForTest to route into probeOneAt. Split out so the
// non-Linux build doesn't see probeOneAt at all.
func probeOneAtForTestInvoke(root, name string) ModuleState {
	return probeOneAt(root, name)
}

// SwapProbeStatFnForTest replaces the stat indirection used by
// probeOneAt under probeStatMu. Returns the previous function so
// callers can restore it in t.Cleanup.
func SwapProbeStatFnForTest(fn func(string) (fs.FileInfo, error)) func(string) (fs.FileInfo, error) {
	probeStatMu.Lock()
	defer probeStatMu.Unlock()

	old := probeStatFn

	probeStatFn = fn

	return old
}
