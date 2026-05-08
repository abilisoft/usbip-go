package usbip

import (
	"time"

	internalapp "github.com/abilisoft/usbip-go/internal/app"
)

// BackoffStrategy computes delays between reconnect attempts. Concrete
// types shipped with this package are FixedBackoff and ExponentialBackoff.
// Consumers can implement their own strategy as long as it satisfies
// both methods; AttachOptions.Backoff accepts any implementation.
type BackoffStrategy interface {
	// Next returns the delay to sleep before attempt number `attempt`
	// (0-indexed — attempt 0 is the first retry after the initial
	// failure).
	Next(attempt int) time.Duration

	// Reset returns the strategy to its initial state. Called after a
	// successful reconnect so the next failure starts from the smallest
	// delay again.
	Reset()
}

// FixedBackoff returns the same Delay for every attempt. A zero Delay
// makes every retry fire immediately — correct for deterministic tests
// but never appropriate for production.
type FixedBackoff struct {
	Delay time.Duration
}

// Next returns b.Delay unchanged.
func (b FixedBackoff) Next(_ int) time.Duration { return b.Delay }

// Reset is a no-op for FixedBackoff.
func (FixedBackoff) Reset() {}

// ExponentialBackoff doubles the base delay every attempt, clamps at
// Max, and optionally jitters by ±Jitter. The zero-value is NOT usable
// — construct via NewExponentialBackoff which validates the config and
// seeds a PRNG for jitter.
type ExponentialBackoff struct {
	inner *internalapp.ExponentialBackoff
}

// ExponentialBackoffConfig mirrors the internal config verbatim. Zero-
// value fields take the internal defaults documented per field on the
// internal type.
type ExponentialBackoffConfig struct {
	// Min is the delay for the first attempt (attempt 0). A zero Min
	// collapses every delay to zero, so callers should set this
	// explicitly.
	Min time.Duration

	// Max caps every returned delay. Values larger than Max are clamped.
	Max time.Duration

	// Jitter is the fractional range applied multiplicatively to the
	// base delay. Must be in [0, 1). A zero Jitter disables jitter
	// entirely and Next returns the deterministic geometric value.
	Jitter float64
}

// NewExponentialBackoff constructs an ExponentialBackoff from cfg. The
// returned *ExponentialBackoff is safe for concurrent Next calls.
func NewExponentialBackoff(cfg ExponentialBackoffConfig) *ExponentialBackoff {
	return &ExponentialBackoff{
		inner: internalapp.NewExponentialBackoff(internalapp.ExponentialBackoffConfig{
			Min:    cfg.Min,
			Max:    cfg.Max,
			Jitter: cfg.Jitter,
		}),
	}
}

// Next returns the delay for the given attempt.
func (b *ExponentialBackoff) Next(attempt int) time.Duration {
	return b.inner.Next(attempt)
}

// Reset clears attempt-dependent state; a no-op for ExponentialBackoff
// since its Next is a pure function of attempt.
func (b *ExponentialBackoff) Reset() { b.inner.Reset() }

// internalBackoffAdapter wraps any public BackoffStrategy so it
// satisfies the internal interface. The adapter is transparent: calls
// forward 1:1 without added state.
type internalBackoffAdapter struct {
	pub BackoffStrategy
}

// Next forwards to the wrapped strategy.
func (a internalBackoffAdapter) Next(attempt int) time.Duration { return a.pub.Next(attempt) }

// Reset forwards to the wrapped strategy.
func (a internalBackoffAdapter) Reset() { a.pub.Reset() }

// backoffToInternal translates a public BackoffStrategy to the internal
// interface. A nil input maps to nil so AttachOptions.Backoff can stay
// optional. Native internal types (FixedBackoff, *ExponentialBackoff)
// are unwrapped to skip the forwarding layer; everything else goes
// through the adapter.
func backoffToInternal(b BackoffStrategy) internalapp.BackoffStrategy {
	if b == nil {
		return nil
	}

	switch v := b.(type) {
	case FixedBackoff:
		return internalapp.FixedBackoff{Delay: v.Delay}
	case *FixedBackoff:
		return internalapp.FixedBackoff{Delay: v.Delay}
	case *ExponentialBackoff:
		return v.inner
	}

	return internalBackoffAdapter{pub: b}
}
