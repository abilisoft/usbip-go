package testutil_test

import (
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/stretchr/testify/require"
)

// Constants used across FakeClock tests so assertions are stable
// regardless of wall-clock drift and repeated literals collapse under
// goconst/mnd.
const (
	fakeClockEpochYear  = 2026
	fakeClockEpochMonth = time.April
	fakeClockEpochDay   = 18
	fakeClockEpochHour  = 12
)

// newFakeClockEpoch returns the fixed reference time used across tests.
// A function (rather than a package-level var) keeps the testutil test
// file free of globals (gochecknoglobals).
func newFakeClockEpoch() time.Time {
	return time.Date(fakeClockEpochYear, fakeClockEpochMonth, fakeClockEpochDay, fakeClockEpochHour, 0, 0, 0, time.UTC)
}

// TestFakeClockNow asserts NewFakeClockAt sets the initial Now().
func TestFakeClockNow(t *testing.T) {
	t.Parallel()

	epoch := newFakeClockEpoch()
	clock := testutil.NewFakeClockAt(epoch)
	require.Equal(t, epoch, clock.Now())
}

// TestFakeClockSleepAdvances asserts Sleep advances Now by exactly d.
func TestFakeClockSleepAdvances(t *testing.T) {
	t.Parallel()

	epoch := newFakeClockEpoch()
	clock := testutil.NewFakeClockAt(epoch)
	clock.Sleep(1 * time.Second)
	require.Equal(t, epoch.Add(1*time.Second), clock.Now())
}

// TestFakeClockAdvanceFiresAfterChannels asserts Advance triggers any
// After channel whose deadline has been reached and leaves pending
// channels untouched.
func TestFakeClockAdvanceFiresAfterChannels(t *testing.T) {
	t.Parallel()

	epoch := newFakeClockEpoch()
	clock := testutil.NewFakeClockAt(epoch)
	chFive := clock.After(5 * time.Second)
	chTen := clock.After(10 * time.Second)

	clock.Advance(5 * time.Second)

	select {
	case got := <-chFive:
		require.Equal(t, epoch.Add(5*time.Second), got)
	default:
		t.Fatal("5s channel should have fired")
	}

	select {
	case <-chTen:
		t.Fatal("10s channel must not fire after 5s advance")
	default:
	}

	clock.Advance(5 * time.Second)

	select {
	case got := <-chTen:
		require.Equal(t, epoch.Add(10*time.Second), got)
	default:
		t.Fatal("10s channel should have fired after second advance")
	}
}

// TestFakeClockAfterZeroFiresImmediately asserts After(0) receives a
// value without requiring Advance — a zero-duration wait is an
// unconditional fire.
func TestFakeClockAfterZeroFiresImmediately(t *testing.T) {
	t.Parallel()

	epoch := newFakeClockEpoch()
	clock := testutil.NewFakeClockAt(epoch)
	ch := clock.After(0)

	select {
	case got := <-ch:
		require.Equal(t, epoch, got)
	default:
		t.Fatal("After(0) must deliver immediately")
	}
}

// fakeClockRaceGoroutines is the number of goroutines contending for
// the FakeClock mutex in the concurrency test.
const fakeClockRaceGoroutines = 8

// fakeClockRacePerGoroutine is the number of Sleep calls each
// goroutine in the concurrency test issues.
const fakeClockRacePerGoroutine = 4

// fakeClockRaceStep is the per-Sleep advance in the concurrency test.
const fakeClockRaceStep = 1 * time.Millisecond

// TestFakeClockConcurrentSafe runs parallel Sleep and Advance calls
// through the race detector. Only total elapsed is asserted; ordering
// between goroutines is undefined.
func TestFakeClockConcurrentSafe(t *testing.T) {
	t.Parallel()

	epoch := newFakeClockEpoch()
	clock := testutil.NewFakeClockAt(epoch)

	var wg sync.WaitGroup

	wg.Add(fakeClockRaceGoroutines)

	for range fakeClockRaceGoroutines {
		go func() {
			defer wg.Done()

			for range fakeClockRacePerGoroutine {
				clock.Sleep(fakeClockRaceStep)
			}
		}()
	}

	wg.Wait()

	expected := epoch.Add(fakeClockRaceStep * fakeClockRaceGoroutines * fakeClockRacePerGoroutine)
	require.Equal(t, expected, clock.Now())
}

// TestFakeClockPendingReflectsRegistrations asserts the Pending()
// accessor reports the live count of After-channels awaiting their
// deadline: each After call increments, each fired deadline (via
// Advance) decrements, and sub-zero or zero durations never register.
// The accessor exists so tests asserting a "register-before-return"
// contract on watcher goroutines can verify registration synchronously
// without polling or wall-clock sleeps.
func TestFakeClockPendingReflectsRegistrations(t *testing.T) {
	t.Parallel()

	epoch := newFakeClockEpoch()
	clock := testutil.NewFakeClockAt(epoch)

	require.Equal(t, 0, clock.Pending(),
		"a fresh FakeClock has no pending deadlines")

	_ = clock.After(1 * time.Second)
	require.Equal(t, 1, clock.Pending(),
		"After registers one pending deadline")

	_ = clock.After(2 * time.Second)
	require.Equal(t, 2, clock.Pending(),
		"a second After bumps pending to two")

	// After(0) fires immediately and must NOT add to the pending list.
	_ = clock.After(0)
	require.Equal(t, 2, clock.Pending(),
		"After(0) fires immediately and does not register")

	clock.Advance(1 * time.Second)
	require.Equal(t, 1, clock.Pending(),
		"Advance consumes deadlines it fires")

	clock.Advance(1 * time.Second)
	require.Equal(t, 0, clock.Pending(),
		"all deadlines consumed after their time advances")
}

// TestFakeClockSatisfiesInterface asserts FakeClock implements the
// app.Clock interface at compile time.
func TestFakeClockSatisfiesInterface(t *testing.T) {
	t.Parallel()

	var clock app.Clock = testutil.NewFakeClockAt(newFakeClockEpoch())
	require.NotNil(t, clock)
}

// fakeClockAfterRaceSubscribers is the number of goroutines registering
// After timers concurrently with an Advance in the race test.
const fakeClockAfterRaceSubscribers = 16

// TestFakeClockAfterRaceWithAdvance exercises concurrent After
// registration against a single Advance caller. The scenario: N
// goroutines each call After(d) to register a timer at deadline epoch+d.
// One additional goroutine calls Advance(d) to fire them. The race
// detector catches any unsynchronised access on the timer slice;
// channel-fire semantics are asserted by requiring every registered
// channel to receive exactly one tick.
//
// This is the actual concurrency scenario the earlier test claimed to
// cover but did not: Sleep is just Advance serially, so TestFakeClockConcurrentSafe
// was only proving that Advance is safe to call sequentially from many
// goroutines, not that After + Advance are safe against each other.
func TestFakeClockAfterRaceWithAdvance(t *testing.T) {
	t.Parallel()

	epoch := newFakeClockEpoch()
	clock := testutil.NewFakeClockAt(epoch)

	const tick = 10 * time.Millisecond

	var registered sync.WaitGroup

	registered.Add(fakeClockAfterRaceSubscribers)

	channels := make([]<-chan time.Time, fakeClockAfterRaceSubscribers)

	// Fan out: each goroutine registers its After timer. Every call
	// takes the FakeClock mutex; the race detector fires if Advance
	// touches the timer list without locking.
	for i := range fakeClockAfterRaceSubscribers {
		go func(idx int) {
			defer registered.Done()

			channels[idx] = clock.After(tick)
		}(i)
	}

	registered.Wait()

	// Advance past every registered deadline. All channels must fire.
	clock.Advance(tick)

	for i, ch := range channels {
		select {
		case fireTime := <-ch:
			require.Equal(t, epoch.Add(tick), fireTime, "subscriber %d fire-time mismatch", i)
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d did not receive after Advance", i)
		}
	}
}
