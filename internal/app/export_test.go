// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"net"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// PublishSessionEventForTest exposes the internal session-event fan-out
// so race-detector tests can drive the publish path directly and
// reproduce the publish-vs-unsubscribe window.
func PublishSessionEventForTest(e *Exporter, ev domain.Event) {
	e.publishSessionEvent(ev)
}

// ArmHandshakeTimeoutForTest exposes the handshake-timeout arming helper
// so black-box tests can assert the "register timer before return"
// invariant directly. The returned stop func disarms the watcher and
// blocks until the spawned goroutine exits — callers MUST invoke it
// exactly once to avoid a test-scoped goroutine leak.
func ArmHandshakeTimeoutForTest(e *Exporter, conn net.Conn) func() {
	return e.armHandshakeTimeout(&connCloser{conn: conn})
}

// SessionSubscribersLenForTest reports the live count of WatchSessions
// fanout subscribers under the exporter's read lock. Black-box tests
// use it to pin the lazy-registration contract: a Watch caller that
// constructs the iter and discards it must not occupy a fanout slot.
func SessionSubscribersLenForTest(e *Exporter) int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	return len(e.subscribers)
}

// ImporterSubscribersLenForTest is the Importer counterpart of
// SessionSubscribersLenForTest. Pins the same lazy-registration
// contract on Importer.Watch.
func ImporterSubscribersLenForTest(i *Importer) int {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return len(i.subscribers)
}
