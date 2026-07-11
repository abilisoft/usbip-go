// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// BackoffStrategy computes the sleep interval between reconnect
// attempts. Concrete implementations are FixedBackoff and
// ExponentialBackoff. See importer-lifecycle OpenSpec.
type BackoffStrategy interface {
	// Next returns the delay to sleep before attempt number `attempt`
	// (0-indexed — attempt 0 is the first retry after the initial
	// failure). Implementations MAY ignore the attempt number (FixedBackoff)
	// or use it as an exponent (ExponentialBackoff).
	Next(attempt int) time.Duration

	// Reset returns the strategy to its initial state. Called after
	// a successful reconnect so the next failure starts from the
	// smallest delay again.
	Reset()
}

// FixedBackoff returns the same Delay for every attempt. Reset is a
// no-op because FixedBackoff carries no attempt state.
type FixedBackoff struct {
	Delay time.Duration
}

// Next returns b.Delay unchanged.
func (b FixedBackoff) Next(_ int) time.Duration { return b.Delay }

// Reset is a no-op for FixedBackoff.
func (FixedBackoff) Reset() {}

// ExponentialBackoffConfig configures an ExponentialBackoff at
// construction time. Zero-value fields take the defaults documented
// per field.
type ExponentialBackoffConfig struct {
	// Min is the delay for the first attempt (attempt 0). Required —
	// a zero Min would collapse every delay to zero.
	Min time.Duration

	// Max caps every returned delay. Values larger than Max are
	// clamped down. Required.
	Max time.Duration

	// Jitter is the fractional range applied multiplicatively to the
	// base delay: the returned delay is sampled uniformly from
	// [base*(1-Jitter), base*(1+Jitter)]. Must be in [0, 1). A zero
	// Jitter disables jitter entirely and Next returns the deterministic
	// geometric value.
	Jitter float64

	// Rand is the PRNG source for jitter sampling. Tests inject a
	// seeded *rand.Rand for deterministic output; production code
	// leaves it nil and the constructor supplies a fresh random
	// source. Ignored when Jitter == 0.
	Rand *rand.Rand
}

// ExponentialBackoff doubles the base delay every attempt, clamps at
// Max, and optionally jitters by ±Jitter. The structure is safe for
// concurrent Next calls: the internal PRNG access is serialised by a
// mutex because *rand.Rand is not safe for concurrent use.
type ExponentialBackoff struct {
	min, max time.Duration
	jitter   float64

	rngMu sync.Mutex
	rng   *rand.Rand
}

// NewExponentialBackoff constructs a pointer-receiver ExponentialBackoff
// from cfg. When cfg.Rand is nil and cfg.Jitter > 0, a fresh PRNG is
// seeded from the global rand source so production callers don't have
// to wire one in.
func NewExponentialBackoff(cfg ExponentialBackoffConfig) *ExponentialBackoff {
	b := &ExponentialBackoff{
		min:    cfg.Min,
		max:    cfg.Max,
		jitter: cfg.Jitter,
		rng:    cfg.Rand,
	}

	if b.rng == nil && b.jitter > 0 {
		//nolint:gosec // backoff jitter is not a security primitive
		b.rng = rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))
	}

	return b
}

// expBackoffGrowthBase is the geometric growth factor applied per
// attempt (Min * growthBase^attempt).
const expBackoffGrowthBase = 2.0

// Next returns the delay for the given attempt. The delay is
// Min * 2^attempt, clamped to Max, then optionally jittered.
// Negative attempts are treated as zero so the first retry always
// sleeps at least Min (before jitter).
func (b *ExponentialBackoff) Next(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	base := b.baseDelay(attempt)

	if b.jitter == 0 {
		return base
	}

	return b.applyJitter(base)
}

// Reset clears attempt-dependent state. ExponentialBackoff stores no
// per-attempt state (Next is a pure function of its input), so Reset
// is a no-op.
func (*ExponentialBackoff) Reset() {}

// baseDelay computes Min * 2^attempt clamped to Max. Shift-based math
// would overflow for large attempt values; float64 math sidesteps that
// without silently wrapping.
func (b *ExponentialBackoff) baseDelay(attempt int) time.Duration {
	scaled := float64(b.min) * math.Pow(expBackoffGrowthBase, float64(attempt))

	if scaled >= float64(b.max) {
		return b.max
	}

	return time.Duration(scaled)
}

// applyJitter samples a uniform multiplier in [1-jitter, 1+jitter] and
// returns the scaled delay.
func (b *ExponentialBackoff) applyJitter(base time.Duration) time.Duration {
	b.rngMu.Lock()
	defer b.rngMu.Unlock()

	// Float64() is [0, 1); map to [-1, 1) then scale by jitter.
	mult := 1 + b.jitter*(2*b.rng.Float64()-1)

	return time.Duration(float64(base) * mult)
}
