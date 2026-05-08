// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

//go:build conformance_linux

package conformance_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain runs goleak.VerifyTestMain so any goroutine leak in the
// synthetic upstream (accept loop, handler) or in our Importer's
// Attach path (transport Dial keepalives, event subscription) surfaces
// at suite teardown. The conformance suite is hosted-CI friendly, so
// the leak assertion is a cheap invariant check on every run.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
