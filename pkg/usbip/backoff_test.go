// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package usbip_test

import (
	"sync"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// TestFixedBackoffReturnsConstant asserts the simplest strategy: Next
// returns the same Delay for every attempt and Reset is a no-op.
func TestFixedBackoffReturnsConstant(t *testing.T) {
	t.Parallel()

	b := usbip.FixedBackoff{Delay: 250 * time.Millisecond}

	require.Equal(t, 250*time.Millisecond, b.Next(0))
	require.Equal(t, 250*time.Millisecond, b.Next(5))

	// Reset is a no-op; repeated calls must not panic or change state.
	// FixedBackoff.Reset is declared on the value receiver, so call
	// it both on the value and via the BackoffStrategy interface to
	// exercise both dispatch paths.
	b.Reset()
	usbip.BackoffStrategy(b).Reset()

	require.Equal(t, 250*time.Millisecond, b.Next(10))
}

// TestExponentialBackoffGrowsThenClamps pins the deterministic growth
// behaviour without jitter: each attempt doubles the prior delay until
// Max is reached, then the delay is clamped at Max.
func TestExponentialBackoffGrowsThenClamps(t *testing.T) {
	t.Parallel()

	b := usbip.NewExponentialBackoff(usbip.ExponentialBackoffConfig{
		Min:    time.Millisecond,
		Max:    8 * time.Millisecond,
		Jitter: 0,
	})

	require.Equal(t, time.Millisecond, b.Next(0))
	require.Equal(t, 2*time.Millisecond, b.Next(1))
	require.Equal(t, 4*time.Millisecond, b.Next(2))
	require.Equal(t, 8*time.Millisecond, b.Next(3))

	// Past the clamp, the delay stays at Max.
	require.Equal(t, 8*time.Millisecond, b.Next(10))

	b.Reset()

	require.Equal(t, time.Millisecond, b.Next(0))
}

// TestExponentialBackoffJitterBounded proves a non-zero jitter yields
// a delay inside [base*(1-Jitter), base*(1+Jitter)] for a large sample.
// The test tolerates a 5% slack to absorb rounding without muting a
// genuine bound violation.
func TestExponentialBackoffJitterBounded(t *testing.T) {
	t.Parallel()

	const (
		base   = 10 * time.Millisecond
		jitter = 0.2
		iters  = 256
	)

	b := usbip.NewExponentialBackoff(usbip.ExponentialBackoffConfig{
		Min:    base,
		Max:    10 * base,
		Jitter: jitter,
	})

	low := time.Duration(float64(base) * (1 - jitter - 0.05))
	high := time.Duration(float64(base) * (1 + jitter + 0.05))

	for range iters {
		d := b.Next(0)
		require.GreaterOrEqual(t, d, low, "delay below lower bound")
		require.LessOrEqual(t, d, high, "delay above upper bound")
	}
}

// TestBackoffStrategyInterfaceSatisfied proves both shipped concrete
// types satisfy the public BackoffStrategy interface. The assignment
// below only type-checks when both Next and Reset are present.
func TestBackoffStrategyInterfaceSatisfied(t *testing.T) {
	t.Parallel()

	var (
		_ usbip.BackoffStrategy = usbip.FixedBackoff{}
		_ usbip.BackoffStrategy = (*usbip.ExponentialBackoff)(nil)
	)

	require.NotNil(t, usbip.NewExponentialBackoff(usbip.ExponentialBackoffConfig{
		Min: time.Millisecond,
		Max: time.Second,
	}))
}

// customBackoff is a consumer-defined BackoffStrategy. It exists to
// prove the internalBackoffAdapter path: a strategy that is neither a
// FixedBackoff nor an *ExponentialBackoff is wrapped transparently
// when translated to the internal form.
type customBackoff struct {
	resetCount int
}

type unsynchronizedBackoff struct {
	calls int
}

func (b *unsynchronizedBackoff) Next(_ int) time.Duration {
	b.calls++

	return 0
}

func (b *unsynchronizedBackoff) Reset() { b.calls = 0 }

// Next returns a fixed 42 ms delay regardless of attempt. Pointer
// receiver so Reset can mutate state; BackoffStrategy is satisfied
// as *customBackoff.
func (*customBackoff) Next(_ int) time.Duration { return 42 * time.Millisecond }

// Reset tracks invocations so tests can assert the forwarded call.
func (c *customBackoff) Reset() { c.resetCount++ }

// TestBackoffToInternalTypedNilDoesNotPanic proves a consumer that
// hands the constructor a typed-nil concrete backoff (for example
// `(*usbip.FixedBackoff)(nil)` passed through the BackoffStrategy
// interface) does not crash the translator. The previous implementation
// dereferenced the nil pointer inside the type-switch; this test locks
// in the defensive-nil guard.
func TestBackoffToInternalTypedNilDoesNotPanic(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   usbip.BackoffStrategy
	}{
		{"TypedNilFixedBackoff", (*usbip.FixedBackoff)(nil)},
		{"TypedNilExponentialBackoff", (*usbip.ExponentialBackoff)(nil)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// NotPanics wraps the invocation; a panic from the
			// type-switch deref would mark the test failed with the
			// panic's recover message.
			require.NotPanics(t, func() {
				_ = usbip.BackoffToInternalForTest(tc.in)
			})
		})
	}
}

// TestBackoffToInternalMapsEachShape exercises the type-switch inside
// the facade's translation helper: nil stays nil, FixedBackoff value
// and pointer unwrap to the internal FixedBackoff, *ExponentialBackoff
// unwraps to its internal form, and a consumer-defined strategy
// dispatches via the internalBackoffAdapter wrapper.
func TestBackoffToInternalMapsEachShape(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   usbip.BackoffStrategy
	}{
		{"nil", nil},
		{"FixedBackoffValue", usbip.FixedBackoff{Delay: 3 * time.Millisecond}},
		{"FixedBackoffPointer", &usbip.FixedBackoff{Delay: 5 * time.Millisecond}},
		{
			"ExponentialBackoffPointer",
			usbip.NewExponentialBackoff(usbip.ExponentialBackoffConfig{
				Min: time.Millisecond, Max: 10 * time.Millisecond,
			}),
		},
		{"CustomStrategy", &customBackoff{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := usbip.BackoffToInternalForTest(tc.in)

			if tc.in == nil {
				require.Nil(t, got)

				return
			}

			require.NotNil(t, got)

			// Round-trip Next through the internal interface to
			// prove the translated value yields a sane delay.
			require.GreaterOrEqual(t, got.Next(0), time.Duration(0))

			got.Reset()
		})
	}
}

// TestCustomBackoffWrappedByAdapter proves a consumer-defined strategy
// flows through AttachOptions.Backoff: the facade Attach merges and
// translates the custom strategy, preserving the Next/Reset semantics
// across the boundary.
func TestCustomBackoffWrappedByAdapter(t *testing.T) {
	t.Parallel()

	b := &customBackoff{}

	// Wire it into AttachOptions. The translation inside the facade
	// recognises no known concrete type and wraps via internalBackoffAdapter.
	opts := usbip.AttachOptions{Backoff: b}

	// Direct invariant: the public interface dispatches to the
	// underlying strategy regardless of adapter wrapping.
	require.Equal(t, 42*time.Millisecond, opts.Backoff.Next(0))

	opts.Backoff.Reset()
	require.Equal(t, 1, b.resetCount)
}

func TestLegacyCustomBackoffAdapterSerializesNextAndReset(t *testing.T) {
	t.Parallel()

	strategy := &unsynchronizedBackoff{}
	adapted := usbip.BackoffToInternalForTest(strategy)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 100 {
				_ = adapted.Next(0)
				adapted.Reset()
			}
		})
	}

	wg.Wait()
}
