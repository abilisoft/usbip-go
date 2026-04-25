// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"context"
	"errors"
	"testing"
	"time"
)

// errNotImplemented is returned by stub methods whose behaviour a
// particular test never exercises. A real call would surface this
// sentinel, making the gap loud rather than silent.
var errNotImplemented = errors.New("stub: not implemented")

// newBoundedCtx builds a one-second bounded child of t.Context(). The
// returned cancel must be installed via t.Cleanup so the test goroutine
// is guaranteed to tear down the exporter's internal subscribers before
// the test exits.
func newBoundedCtx(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()

	return context.WithTimeout(t.Context(), time.Second)
}
