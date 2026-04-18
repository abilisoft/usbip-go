package app_test

import (
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/stretchr/testify/require"
)

// clockTestSleepBudget is the upper bound on how long a RealClock.Sleep(0)
// is allowed to take. 100 ms is generous enough for CI schedulers under
// load while still catching any accidental long sleep.
const clockTestSleepBudget = 100 * time.Millisecond

// TestRealClockNowNonZero asserts RealClock.Now() returns a non-zero
// time value, proving it forwards to stdlib time.Now.
func TestRealClockNowNonZero(t *testing.T) {
	t.Parallel()

	var clock app.RealClock

	got := clock.Now()
	require.False(t, got.IsZero(), "RealClock.Now() must return a non-zero time")
}

// TestRealClockSleepZeroReturnsFast asserts Sleep(0) returns quickly
// (within clockTestSleepBudget).
func TestRealClockSleepZeroReturnsFast(t *testing.T) {
	t.Parallel()

	var clock app.RealClock

	start := time.Now()

	clock.Sleep(0)

	elapsed := time.Since(start)

	require.Less(t, elapsed, clockTestSleepBudget)
}

// TestRealClockAfterFires asserts that After(0) delivers a time value
// without blocking beyond clockTestSleepBudget.
func TestRealClockAfterFires(t *testing.T) {
	t.Parallel()

	var clock app.RealClock

	select {
	case got := <-clock.After(0):
		require.False(t, got.IsZero(), "After channel must deliver a non-zero time")
	case <-time.After(clockTestSleepBudget):
		t.Fatal("RealClock.After(0) did not fire within budget")
	}
}

// TestRealClockSatisfiesInterface checks compile-time that RealClock
// implements the Clock interface. The blank assignment fails to build
// if any method is missing.
func TestRealClockSatisfiesInterface(t *testing.T) {
	t.Parallel()

	var clock app.Clock = app.RealClock{}
	require.NotNil(t, clock.After(0))
}
