package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// swapKernelModuleProbeFn replaces the package-level probe hook under
// lock and returns the previous value so t.Cleanup can restore it.
// Paired with swapKernelModuleProbeClock, the two helpers let a test
// drive the cache TTL without real-time sleeps.
func swapKernelModuleProbeFn(
	fn func(context.Context) (map[string]usbip.ModuleState, error),
) func(context.Context) (map[string]usbip.ModuleState, error) {
	kernelModuleProbeMu.Lock()
	defer kernelModuleProbeMu.Unlock()

	prev := kernelModuleProbeFn

	kernelModuleProbeFn = fn

	return prev
}

// swapKernelModuleProbeClock replaces the clock hook used by
// statusExporter.KernelModules so tests can advance "now" past the
// cache TTL deterministically.
func swapKernelModuleProbeClock(fn func() time.Time) func() time.Time {
	kernelModuleProbeMu.Lock()
	defer kernelModuleProbeMu.Unlock()

	prev := kernelModuleProbeClock

	kernelModuleProbeClock = fn

	return prev
}

// TestKernelModulesCachedWithinTTL proves Phase 8 Finding 5's caching
// contract: statusExporter.KernelModules must NOT call the underlying
// probe on every GET /. Consecutive calls within the cache TTL serve
// the last-known snapshot. First call populates; second call inside
// the TTL MUST NOT re-invoke the probe.
func TestKernelModulesCachedWithinTTL(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	originalFn := swapKernelModuleProbeFn(func(
		_ context.Context,
	) (map[string]usbip.ModuleState, error) {
		calls.Add(1)

		return map[string]usbip.ModuleState{
			"usbip_core": usbip.ModuleStateLoaded,
		}, nil
	})

	t.Cleanup(func() { swapKernelModuleProbeFn(originalFn) })

	s := &statusExporter{}

	// First call — must populate the cache and invoke the probe once.
	mods, err := s.KernelModules(context.Background())
	require.NoError(t, err)
	require.Equal(t, usbip.ModuleStateLoaded, mods["usbip_core"])
	require.EqualValues(t, 1, calls.Load(),
		"first call must invoke probe exactly once")

	// Second call within TTL — MUST be served from cache; no additional
	// invocation of the probe. Running it back-to-back keeps the test
	// hermetic (no time.Sleep needed).
	_, err = s.KernelModules(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load(),
		"second call inside TTL must not re-probe")
}

// TestKernelModulesReprobesAfterTTL proves the cache actually expires:
// after the TTL elapses, KernelModules must re-invoke the underlying
// probe rather than hand back stale data.
func TestKernelModulesReprobesAfterTTL(t *testing.T) {
	t.Parallel()

	var (
		calls atomic.Int32
		now   atomic.Int64
	)

	now.Store(time.Now().UnixNano())

	originalFn := swapKernelModuleProbeFn(func(
		_ context.Context,
	) (map[string]usbip.ModuleState, error) {
		calls.Add(1)

		return map[string]usbip.ModuleState{
			"usbip_core": usbip.ModuleStateLoaded,
		}, nil
	})

	t.Cleanup(func() { swapKernelModuleProbeFn(originalFn) })

	originalClock := swapKernelModuleProbeClock(func() time.Time {
		return time.Unix(0, now.Load())
	})

	t.Cleanup(func() { swapKernelModuleProbeClock(originalClock) })

	s := &statusExporter{}

	_, err := s.KernelModules(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, calls.Load())

	// Advance the fake clock past the 5-second TTL and re-probe.
	now.Add(int64(6 * time.Second))

	_, err = s.KernelModules(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 2, calls.Load(),
		"call past TTL must re-invoke the probe")
}
