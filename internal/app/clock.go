// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import "time"

// Clock abstracts the stdlib time functions the app layer depends on.
// Concrete code uses RealClock; tests inject a deterministic fake from
// internal/testutil.
//
// Spec §5.1: retry/backoff and attach timeouts must be driven by an
// injected clock so tests do not slow down wall time.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Sleep pauses the calling goroutine for at least d.
	Sleep(d time.Duration)
	// After returns a channel that delivers the time after d has elapsed.
	After(d time.Duration) <-chan time.Time
}

// RealClock is the production Clock implementation: every method
// forwards to the stdlib time package. It carries no state and is
// trivially zero-value constructible.
type RealClock struct{}

// Now returns time.Now.
func (RealClock) Now() time.Time { return time.Now() }

// Sleep forwards to time.Sleep.
func (RealClock) Sleep(d time.Duration) { time.Sleep(d) }

// After forwards to time.After.
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
