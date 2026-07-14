// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app_test

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/internal/app"
	"github.com/stretchr/testify/require"
)

// maxUint64Source makes rand.Rand.Float64 sample as close to 1 as the API
// permits, deterministically exercising the largest positive jitter branch.
type maxUint64Source struct{}

func (maxUint64Source) Uint64() uint64 { return math.MaxUint64 }

// backoffJitterSeed returns a deterministic PRNG seed used across the
// jitter tests so failures reproduce locally without recovering the
// original random stream. It is a function rather than a package-level
// var to satisfy gochecknoglobals while keeping the seed literal in
// one place.
func backoffJitterSeed() [32]byte {
	return [32]byte{
		0x5e, 0xed, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05,
		0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d,
		0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15,
		0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d,
	}
}

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

	rng := rand.New(rand.NewChaCha8(backoffJitterSeed()))

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

// TestExponentialBackoffCapsFinalJitter proves Max bounds the emitted delay,
// not only the pre-jitter base. The deterministic high sample would otherwise
// turn an 8 ns base into almost 12 ns and violate the documented 10 ns cap.
func TestExponentialBackoffCapsFinalJitter(t *testing.T) {
	t.Parallel()

	b := app.NewExponentialBackoff(app.ExponentialBackoffConfig{
		Min:    8,
		Max:    10,
		Jitter: 0.5,
		Rand:   rand.New(maxUint64Source{}),
	})

	require.Equal(t, 10*time.Nanosecond, b.Next(0))
}

// TestExponentialBackoffJitterNearDurationLimitDoesNotOverflow covers the
// numeric boundary where float64(MaxInt64)*positive-jitter is outside the
// time.Duration range. Capping before conversion must return Max rather than a
// wrapped or implementation-dependent negative duration.
func TestExponentialBackoffJitterNearDurationLimitDoesNotOverflow(t *testing.T) {
	t.Parallel()

	maxDuration := time.Duration(math.MaxInt64)
	b := app.NewExponentialBackoff(app.ExponentialBackoffConfig{
		Min:    maxDuration,
		Max:    maxDuration,
		Jitter: 0.5,
		Rand:   rand.New(maxUint64Source{}),
	})

	require.Equal(t, maxDuration, b.Next(0))
}

// TestExponentialBackoffJitterDeterministic asserts a seeded PRNG
// reproduces the same sequence across calls.
func TestExponentialBackoffJitterDeterministic(t *testing.T) {
	t.Parallel()

	mk := func() *app.ExponentialBackoff {
		rng := rand.New(rand.NewChaCha8(backoffJitterSeed()))

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

// TestFixedBackoffResetIsNoop pins the per-doc contract: FixedBackoff
// has no per-attempt state (Next(attempt) = Delay for every attempt),
// so Reset is a no-op. The test calls Reset and verifies subsequent
// Next calls return the same Delay.
func TestFixedBackoffResetIsNoop(t *testing.T) {
	t.Parallel()

	b := app.FixedBackoff{Delay: 250 * time.Millisecond}

	require.Equal(t, 250*time.Millisecond, b.Next(0))
	require.Equal(t, 250*time.Millisecond, b.Next(5))

	b.Reset()

	require.Equal(t, 250*time.Millisecond, b.Next(0))
	require.Equal(t, 250*time.Millisecond, b.Next(5))
}

// TestExponentialBackoffResetIsNoop locks the same contract for the
// exponential variant: Next is a pure function of attempt, so Reset
// must not change behavior. The test exercises Reset twice in a row
// to also pin idempotency.
func TestExponentialBackoffResetIsNoop(t *testing.T) {
	t.Parallel()

	b := app.NewExponentialBackoff(app.ExponentialBackoffConfig{
		Min: time.Second,
		Max: time.Minute,
	})

	beforeReset := b.Next(2)

	b.Reset()
	b.Reset()

	require.Equal(t, beforeReset, b.Next(2),
		"ExponentialBackoff.Next must be a pure function of attempt; Reset must not perturb it")
}
