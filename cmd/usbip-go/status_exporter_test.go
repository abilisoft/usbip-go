// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
	"github.com/stretchr/testify/require"
)

// newStatusExporterForTest constructs a statusExporter with just the
// fields KernelModules reads. Leaving exp / listenAddr / atomic flags
// at their zero values is intentional: these tests exercise only the
// TTL-cache path, and statusSource methods that dereference exp are
// never invoked. kernelModuleProbe and kernelModuleClock default to
// the production hooks, which tests override via setKernelModuleProbe
// / setKernelModuleClock.
func newStatusExporterForTest() *statusExporter {
	return &statusExporter{
		kernelModuleProbe: defaultKernelModuleProbe,
		kernelModuleClock: time.Now,
	}
}

// TestKernelModulesCachedWithinTTL pins the kernel-module probe
// caching contract: statusExporter.KernelModules must NOT call the
// underlying probe on every GET /. Consecutive calls within the
// cache TTL serve the last-known snapshot. First call populates;
// second call inside the TTL MUST NOT re-invoke the probe.
//
// Per-instance injection (not package globals) keeps this test
// deterministic under -count=N -race even when TestKernelModulesReprobesAfterTTL
// schedules concurrently.
func TestKernelModulesCachedWithinTTL(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	s := newStatusExporterForTest()
	s.setKernelModuleProbe(func(
		_ context.Context,
	) (map[string]usbip.ModuleState, error) {
		calls.Add(1)

		return map[string]usbip.ModuleState{
			testUSBIPCoreModule: usbip.ModuleStateLoaded,
		}, nil
	})

	// First call — must populate the cache and invoke the probe once.
	mods, err := s.KernelModules(context.Background())
	require.NoError(t, err)
	require.Equal(t, usbip.ModuleStateLoaded, mods[testUSBIPCoreModule])
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
// probe rather than hand back stale data. Per-instance clock injection
// means the Advance below cannot affect any other test's cache expiry.
func TestKernelModulesReprobesAfterTTL(t *testing.T) {
	t.Parallel()

	var (
		calls atomic.Int32
		now   atomic.Int64
	)

	now.Store(time.Now().UnixNano())

	s := newStatusExporterForTest()
	s.setKernelModuleProbe(func(
		_ context.Context,
	) (map[string]usbip.ModuleState, error) {
		calls.Add(1)

		return map[string]usbip.ModuleState{
			testUSBIPCoreModule: usbip.ModuleStateLoaded,
		}, nil
	})
	s.setKernelModuleClock(func() time.Time {
		return time.Unix(0, now.Load())
	})

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
