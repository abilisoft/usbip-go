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

// TestFakeClockSatisfiesInterface asserts FakeClock implements the
// app.Clock interface at compile time.
func TestFakeClockSatisfiesInterface(t *testing.T) {
	t.Parallel()

	var clock app.Clock = testutil.NewFakeClockAt(newFakeClockEpoch())
	require.NotNil(t, clock)
}
