package testutil

import (
	"sync"
	"time"
)

// fakeClockChanBuffer sizes the buffered channel returned by FakeClock.After
// so the fire goroutine can deliver the current time without blocking on a
// receiver that is still setting up its select.
const fakeClockChanBuffer = 1

// FakeClock is a deterministic app.Clock implementation for tests. All
// internal state is guarded by a mutex so concurrent Sleep, After, and
// Advance calls are race-free under the Go race detector.
//
// Use NewFakeClockAt to construct. The zero value is intentionally not
// useful because every deterministic test wants an explicit epoch.
//
// FakeClock satisfies app.Clock. The assertion lives here rather than in
// internal/app to avoid a circular dependency: internal/testutil consumes
// the Clock contract, and internal/app must not import testutil in
// production code.
type FakeClock struct {
	mu      sync.Mutex
	now     time.Time
	pending []fakePending
}

// fakePending is a single After-channel awaiting its deadline.
type fakePending struct {
	deadline time.Time
	ch       chan time.Time
}

// NewFakeClockAt returns a FakeClock whose Now() starts at t.
func NewFakeClockAt(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

// Now returns the clock's current time.
func (f *FakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.now
}

// Sleep advances the clock by d, firing any pending After channels whose
// deadline has been reached.
func (f *FakeClock) Sleep(d time.Duration) {
	f.Advance(d)
}

// After returns a buffered channel that receives the clock's time once
// the deadline (now+d) has been reached via Advance or Sleep. d <= 0
// fires immediately.
func (f *FakeClock) After(d time.Duration) <-chan time.Time {
	ch := make(chan time.Time, fakeClockChanBuffer)

	f.mu.Lock()
	defer f.mu.Unlock()

	if d <= 0 {
		ch <- f.now

		return ch
	}

	f.pending = append(f.pending, fakePending{
		deadline: f.now.Add(d),
		ch:       ch,
	})

	return ch
}

// Pending reports the number of After-channels currently awaiting their
// deadline. Exposed so tests asserting the "register-before-return"
// contract of a watcher goroutine can verify registration synchronously
// without falling back to polling or wall-clock sleeps. The count is
// taken under the FakeClock mutex so concurrent After/Advance callers
// cannot observe a torn value.
func (f *FakeClock) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.pending)
}

// Advance moves the clock forward by d. Any After channel whose deadline
// is at or before the new Now is fired with the new Now value and removed
// from the pending list.
func (f *FakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.now = f.now.Add(d)

	remaining := f.pending[:0]

	for _, p := range f.pending {
		if !p.deadline.After(f.now) {
			p.ch <- f.now

			continue
		}

		remaining = append(remaining, p)
	}

	f.pending = remaining
}
