//go:build linux

package kernel_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain once after every test in the
// package has finished so any goroutine leaked by a Subscribe that
// never releases its ctx-watcher (or any other background worker) is
// caught at suite teardown. Per-test goleak.VerifyNone does not
// compose with t.Parallel() because siblings appear as live goroutines
// to any Verify call; TestMain fires after all parallel tests drain so
// genuine leaks surface without false positives from parallel test
// infrastructure.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
