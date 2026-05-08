package testutil_test

import (
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/abilisoft/usbip-go/internal/testutil"
	"github.com/stretchr/testify/require"
)

// fakeClockEpoch is a fixed reference time used across FakeClock tests
// so assertions are stable regardless of wall-clock drift.
var fakeClockEpoch = time.Date(2026, time.April, 18, 12, 0, 0, 0, time.UTC)

// TestFakeClockNow asserts NewFakeClockAt sets the initial Now().
func TestFakeClockNow(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClockAt(fakeClockEpoch)
	require.Equal(t, fakeClockEpoch, clock.Now())
}

// TestFakeClockSleepAdvances asserts Sleep advances Now by exactly d.
func TestFakeClockSleepAdvances(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClockAt(fakeClockEpoch)
	clock.Sleep(1 * time.Second)
	require.Equal(t, fakeClockEpoch.Add(1*time.Second), clock.Now())
}

// TestFakeClockAdvanceFiresAfterChannels asserts Advance triggers any
// After channel whose deadline has been reached and leaves pending
// channels untouched.
func TestFakeClockAdvanceFiresAfterChannels(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClockAt(fakeClockEpoch)
	chFive := clock.After(5 * time.Second)
	chTen := clock.After(10 * time.Second)

	clock.Advance(5 * time.Second)

	select {
	case got := <-chFive:
		require.Equal(t, fakeClockEpoch.Add(5*time.Second), got)
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
		require.Equal(t, fakeClockEpoch.Add(10*time.Second), got)
	default:
		t.Fatal("10s channel should have fired after second advance")
	}
}

// TestFakeClockAfterZeroFiresImmediately asserts After(0) receives a
// value without requiring Advance — a zero-duration wait is an
// unconditional fire.
func TestFakeClockAfterZeroFiresImmediately(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClockAt(fakeClockEpoch)
	ch := clock.After(0)

	select {
	case got := <-ch:
		require.Equal(t, fakeClockEpoch, got)
	default:
		t.Fatal("After(0) must deliver immediately")
	}
}

// TestFakeClockConcurrentSafe runs parallel Sleep and Advance calls
// through the race detector. Only total elapsed is asserted; ordering
// between goroutines is undefined.
func TestFakeClockConcurrentSafe(t *testing.T) {
	t.Parallel()

	clock := testutil.NewFakeClockAt(fakeClockEpoch)

	var wg sync.WaitGroup
	const goroutines = 8
	const perGoroutine = 4
	const step = 1 * time.Millisecond

	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				clock.Sleep(step)
			}
		}()
	}

	wg.Wait()

	expected := fakeClockEpoch.Add(step * goroutines * perGoroutine)
	require.Equal(t, expected, clock.Now())
}

// TestFakeClockSatisfiesInterface asserts FakeClock implements the
// app.Clock interface at compile time.
func TestFakeClockSatisfiesInterface(t *testing.T) {
	t.Parallel()

	var clock app.Clock = testutil.NewFakeClockAt(fakeClockEpoch)
	require.NotNil(t, clock)
}
