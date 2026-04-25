// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain once after every test in the
// package has finished. Per-test goleak.VerifyNone does not compose
// with t.Parallel() because siblings appear as live goroutines to
// any Verify call; the TestMain hook fires after all parallel tests
// drain, so a genuine leak from Importer's watcher goroutines or
// event fan-out still surfaces, without false positives from parallel
// test infrastructure.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
