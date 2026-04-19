package app_test

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/stretchr/testify/require"
)

// backoffJitterSeed is a deterministic PRNG seed used across the jitter
// tests so failures reproduce locally without recovering the original
// random stream.
const backoffJitterSeed uint64 = 0x5eed_5eed_5eed_5eed

// TestFixedBackoffReturnsConstantDelay asserts FixedBackoff.Next is
// constant regardless of the attempt number.
func TestFixedBackoffReturnsConstantDelay(t *testing.T) {
	t.Parallel()

	b := app.FixedBackoff{Delay: 5 * time.Second}

	require.Equal(t, 5*time.Second, b.Next(0))
	require.Equal(t, 5*time.Second, b.Next(1))
	require.Equal(t, 5*time.Second, b.Next(42))
}

// TestFixedBackoffReset is a no-op semantically but MUST be callable to
// satisfy the BackoffStrategy contract.
func TestFixedBackoffReset(t *testing.T) {
	t.Parallel()

	b := app.FixedBackoff{Delay: time.Second}
	b.Reset()
	require.Equal(t, time.Second, b.Next(0))
}

// TestExponentialBackoffDoublesUntilCap asserts the geometric progression
// Min * 2^attempt, capped at Max, with Jitter disabled.
func TestExponentialBackoffDoublesUntilCap(t *testing.T) {
	t.Parallel()

	b := app.NewExponentialBackoff(app.ExponentialBackoffConfig{
		Min: time.Second,
		Max: 60 * time.Second,
	})

	require.Equal(t, 1*time.Second, b.Next(0))
	require.Equal(t, 2*time.Second, b.Next(1))
	require.Equal(t, 16*time.Second, b.Next(4))
	require.Equal(t, 60*time.Second, b.Next(99))
}

// TestExponentialBackoffJitterBounds asserts jittered output stays
// within [base*(1-j), base*(1+j)] for every attempt, using a seeded
// PRNG for determinism.
func TestExponentialBackoffJitterBounds(t *testing.T) {
	t.Parallel()

	const jitter = 0.25

	rng := rand.New(rand.NewPCG(backoffJitterSeed, backoffJitterSeed)) //nolint:gosec // deterministic test source, not crypto

	b := app.NewExponentialBackoff(app.ExponentialBackoffConfig{
		Min:    time.Second,
		Max:    60 * time.Second,
		Jitter: jitter,
		Rand:   rng,
	})

	for attempt := range 10 {
		base := time.Duration(min(float64(time.Second)*float64(int(1)<<attempt), float64(60*time.Second)))
		lo := time.Duration(float64(base) * (1 - jitter))
		hi := time.Duration(float64(base) * (1 + jitter))

		got := b.Next(attempt)

		require.GreaterOrEqualf(t, got, lo, "attempt %d: got %s < lo %s", attempt, got, lo)
		require.LessOrEqualf(t, got, hi, "attempt %d: got %s > hi %s", attempt, got, hi)
	}
}

// TestExponentialBackoffJitterDeterministic asserts a seeded PRNG
// reproduces the same sequence across calls.
func TestExponentialBackoffJitterDeterministic(t *testing.T) {
	t.Parallel()

	mk := func() *app.ExponentialBackoff {
		rng := rand.New(rand.NewPCG(backoffJitterSeed, backoffJitterSeed)) //nolint:gosec // deterministic test source

		return app.NewExponentialBackoff(app.ExponentialBackoffConfig{
			Min:    time.Second,
			Max:    60 * time.Second,
			Jitter: 0.5,
			Rand:   rng,
		})
	}

	a := mk()
	b := mk()

	for attempt := range 8 {
		require.Equal(t, a.Next(attempt), b.Next(attempt))
	}
}

// TestExponentialBackoffNegativeAttemptClampsToMin asserts a negative
// attempt is treated as zero so the first retry never goes below Min.
func TestExponentialBackoffNegativeAttemptClampsToMin(t *testing.T) {
	t.Parallel()

	b := app.NewExponentialBackoff(app.ExponentialBackoffConfig{
		Min: time.Second,
		Max: time.Minute,
	})
	require.Equal(t, time.Second, b.Next(-1))
}

// TestExponentialBackoffDefaultJitterRand asserts the constructor
// supplies a working PRNG when Rand is nil so callers do not have to
// wire one in production.
func TestExponentialBackoffDefaultJitterRand(t *testing.T) {
	t.Parallel()

	b := app.NewExponentialBackoff(app.ExponentialBackoffConfig{
		Min:    time.Second,
		Max:    time.Minute,
		Jitter: 0.1,
	})

	got := b.Next(0)
	require.GreaterOrEqual(t, got, time.Duration(float64(time.Second)*0.9))
	require.LessOrEqual(t, got, time.Duration(float64(time.Second)*1.1))
}

// TestBackoffStrategyInterfaceSatisfied checks compile-time that both
// Fixed and Exponential satisfy BackoffStrategy.
func TestBackoffStrategyInterfaceSatisfied(t *testing.T) {
	t.Parallel()

	var s app.BackoffStrategy = app.FixedBackoff{Delay: time.Second}
	require.Equal(t, time.Second, s.Next(0))

	s = app.NewExponentialBackoff(app.ExponentialBackoffConfig{Min: time.Second, Max: time.Minute})
	require.Equal(t, time.Second, s.Next(0))

	s.Reset()
}
