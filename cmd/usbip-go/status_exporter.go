// SPDX-FileCopyrightText: 2026 AbiliSoft
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"maps"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

// kernelModuleProbeTTL is the age beyond which statusExporter.KernelModules
// re-invokes the underlying probe. Five seconds is a conservative
// ceiling: long enough that a GET-flooded status endpoint doesn't
// hammer /sys/module on every request, short enough that an operator
// running `modprobe usbip_core` observes the change without
// restarting the daemon.
const kernelModuleProbeTTL = 5 * time.Second

// kernelModuleProbeFunc is the signature of the indirection through
// which statusExporter queries the tri-state kernel-module map.
// Production wiring is defaultKernelModuleProbe; tests supply a mock
// via setKernelModuleProbe to assert call counts and drive the cache
// TTL deterministically without touching /sys.
type kernelModuleProbeFunc func(context.Context) (map[string]usbip.ModuleState, error)

// statusExporter adapts *usbip.Exporter to the statusSource interface
// consumed by serveStatus. It owns the listeningState bookkeeping so
// the status handler can report accepting=true from the moment Serve
// starts its accept loop and accepting=false the instant Serve
// returns or Drain fires.
//
// Each statusExporter owns its kernel-module probe + clock so tests
// running in parallel cannot contaminate each other's probe-invocation
// counts. Previously these were package-level globals swapped under an
// RWMutex; under -count=N parallel scheduling, multiple test instances
// raced to swap the globals and observed probe calls belonging to
// other tests. Per-instance injection eliminates that failure mode
// entirely — the state is scoped to whichever statusExporter the test
// constructed.
type statusExporter struct {
	exp        *usbip.Exporter
	listenAddr string
	activation bool
	accepting  atomic.Bool

	// listenerBound is the operations-observability OpenSpec /readyz input that separates
	// "bind succeeded" from "accept loop processing connections". It
	// flips true as soon as listener.Addr() confirms a non-nil bind
	// and stays true until the daemon exits; accepting flips true
	// only after the first successful Accept.
	listenerBound atomic.Bool

	// drain is the runDaemon-provided callback that cancels the
	// Serve-ctx with a labelled cause so POST /drain can wind down
	// the accept loop without racing Exporter.Shutdown. Stored in an
	// atomic.Pointer so tests that construct statusExporter directly
	// can leave it unset without a nil dereference.
	drain atomic.Pointer[func()]

	// kernelModuleProbe is the tri-state probe invoked from
	// KernelModules. Defaults to defaultKernelModuleProbe; overridden
	// per-instance by tests via setKernelModuleProbe.
	kernelModuleProbe kernelModuleProbeFunc

	// kernelModuleClock supplies "now" for the TTL cache. Defaults to
	// time.Now; overridden per-instance by tests via
	// setKernelModuleClock so cache expiry is deterministic without
	// wall-clock sleeps.
	kernelModuleClock func() time.Time

	// Kernel-module probe cache. kmMu serialises cache updates;
	// kmValue/kmExpiry are accessed under the mutex. The
	// cached map is returned by reference because ModuleState is a
	// value type — operators reading the status JSON cannot mutate
	// through the handle.
	kmMu     sync.Mutex
	kmValue  map[string]usbip.ModuleState
	kmExpiry time.Time
}

// newStatusExporter wires an Exporter + listener into a statusSource.
// activation is the systemd-activation flag rendered under
// listening.activation in the operations-observability and json-contracts
// OpenSpec status JSON; accepting starts false and flips true on the first
// successful Accept of the real listener.
// listenerBound flips true immediately when lis has a non-nil Addr so
// /readyz can distinguish "bind succeeded" from "accept loop actually
// running".
func newStatusExporter(exp *usbip.Exporter, lis net.Listener, activation bool) *statusExporter {
	s := &statusExporter{
		exp:               exp,
		listenAddr:        listenerAddr(lis),
		activation:        activation,
		kernelModuleProbe: defaultKernelModuleProbe,
		kernelModuleClock: time.Now,
	}

	if lis != nil && lis.Addr() != nil {
		s.listenerBound.Store(true)
	}

	return s
}

// listenerAddr extracts a printable address string. On a nil listener
// (unexpected but guarded) it returns an empty string so JSON remains
// well-formed.
func listenerAddr(lis net.Listener) string {
	if lis == nil {
		return ""
	}

	addr := lis.Addr()
	if addr == nil {
		return ""
	}

	return addr.String()
}

// Listening exposes the current accept-path state.
func (s *statusExporter) Listening() listeningState {
	return listeningState{
		Addr:       s.listenAddr,
		Activation: s.activation,
		Accepting:  s.accepting.Load(),
	}
}

// KernelModules reports the module triple required by the
// security-release-quality and operations-observability OpenSpec documents via
// usbip.ProbeKernelModules, with a kernelModuleProbeTTL cache in front of the probe. A cache
// hit avoids a sysfs round-trip and the slog warn that EACCES would
// otherwise log on every poll. Failures from the underlying probe
// bypass the cache so the next call retries.
//
// The mutex is released BEFORE the probe runs so a slow sysfs syscall
// cannot serialise concurrent /readyz callers behind it. Concurrent
// cache-miss callers may all run the probe in parallel; the cache
// write is racy by design (last writer wins) — every probe returns
// the same map for the same kernel state, so the last-writer race
// produces an equivalent result. This trades a tiny amount of
// duplicate work during cache miss for predictable per-caller
// latency under k8s-style sub-second readiness probes.
//
// Probe + clock are read from this statusExporter instance (not a
// package global), so parallel tests constructing their own
// statusExporter with setKernelModuleProbe / setKernelModuleClock
// cannot see each other's invocations.
func (s *statusExporter) KernelModules(ctx context.Context) (map[string]usbip.ModuleState, error) {
	s.kmMu.Lock()
	now := s.kernelModuleClock()

	if s.kmValue != nil && now.Before(s.kmExpiry) {
		out := copyKernelModuleMap(s.kmValue)

		s.kmMu.Unlock()

		return out, nil
	}

	s.kmMu.Unlock()

	mods, err := s.kernelModuleProbe(ctx)
	if err != nil {
		return mods, err
	}

	s.kmMu.Lock()
	// Re-check the cache before writing: another caller's probe may
	// have completed and landed a fresher map between our unlock and
	// re-lock above. Without this guard, a probe started at t=0 that
	// finishes after a probe started at t=1 would overwrite the
	// newer result with stale data.
	winnerNow := s.kernelModuleClock()
	if s.kmValue != nil && winnerNow.Before(s.kmExpiry) {
		out := copyKernelModuleMap(s.kmValue)

		s.kmMu.Unlock()

		return out, nil
	}

	s.kmValue = mods
	s.kmExpiry = winnerNow.Add(kernelModuleProbeTTL)
	s.kmMu.Unlock()

	return copyKernelModuleMap(mods), nil
}

// copyKernelModuleMap returns a shallow copy of the cached map so
// callers mutating the returned value don't corrupt the cache.
func copyKernelModuleMap(src map[string]usbip.ModuleState) map[string]usbip.ModuleState {
	out := make(map[string]usbip.ModuleState, len(src))
	maps.Copy(out, src)

	return out
}

// markAccepting atomically flips the accepting flag rendered under
// listening.accepting. Exposed on *statusExporter (not the interface)
// so run.go can drive transitions from outside the handler.
func (s *statusExporter) markAccepting(v bool) {
	s.accepting.Store(v)
}

// setDrain installs the run-side cancel function called from Drain.
// Stored via atomic.Pointer so tests that construct statusExporter
// without wiring a cancel func see a no-op rather than a nil panic.
func (s *statusExporter) setDrain(fn func()) {
	s.drain.Store(&fn)
}

// setKernelModuleProbe injects a test-owned probe in place of the
// production defaultKernelModuleProbe. Per-instance scoping is what
// lets parallel tests drive their own call-counters without racing a
// shared global.
func (s *statusExporter) setKernelModuleProbe(fn kernelModuleProbeFunc) {
	s.kernelModuleProbe = fn
}

// setKernelModuleClock injects a test-owned clock in place of time.Now.
// Per-instance scoping means an Advance on one test's clock never
// affects another statusExporter's cache expiry.
func (s *statusExporter) setKernelModuleClock(fn func() time.Time) {
	s.kernelModuleClock = fn
}
