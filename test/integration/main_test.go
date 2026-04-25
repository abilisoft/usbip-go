// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build integration_linux

package integration_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain once after every integration
// test has finished. Integration tests spawn real watcher goroutines
// and kernel subscription fan-outs; a leak in one would otherwise land
// on the next unrelated test's doorstep. The TestMain hook fires AFTER
// all parallel tests drain so siblings never appear as false-positive
// leaks in another test's accounting.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
