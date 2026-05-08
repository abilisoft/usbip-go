package main

import (
	"context"
	"fmt"
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
// hammer /sys/module on every request (Finding 5), short enough that
// an operator running `modprobe usbip_core` observes the change
// without restarting the daemon.
const kernelModuleProbeTTL = 5 * time.Second

// kernelModuleProbeFn is the indirection through which statusExporter
// queries the tri-state kernel-module map. Production wiring is
// usbip.ProbeKernelModules; tests replace it to assert call counts and
// drive the cache TTL deterministically without touching /sys.
var (
	kernelModuleProbeFn    = defaultKernelModuleProbe
	kernelModuleProbeClock = time.Now
	kernelModuleProbeMu    sync.RWMutex
)

// defaultKernelModuleProbe wraps usbip.ProbeKernelModules so the
// package-level hook type stays a named function rather than
// accidentally pinning the pkg/usbip symbol as a variable initialiser.
func defaultKernelModuleProbe(ctx context.Context) (map[string]usbip.ModuleState, error) {
	mods, err := usbip.ProbeKernelModules(ctx)
	if err != nil {
		return mods, fmt.Errorf("probe kernel modules: %w", err)
	}

	return mods, nil
}

// currentKernelModuleProbe returns the active probe + clock pair under
// a read lock; tests swapping the hooks serialise with the production
// reader via kernelModuleProbeMu.
func currentKernelModuleProbe() (func(context.Context) (map[string]usbip.ModuleState, error), func() time.Time) {
	kernelModuleProbeMu.RLock()
	defer kernelModuleProbeMu.RUnlock()

	return kernelModuleProbeFn, kernelModuleProbeClock
}

// statusExporter adapts *usbip.Exporter to the statusSource interface
// consumed by serveStatus. It owns the listeningState bookkeeping so
// the status handler can report accepting=true from the moment Serve
// starts its accept loop and accepting=false the instant Serve
// returns or Drain fires.
type statusExporter struct {
	exp        *usbip.Exporter
	listenAddr string
	activation bool
	accepting  atomic.Bool

	// listenerBound is the §11.5.5 /readyz input that separates
	// "bind succeeded" from "accept loop processing connections"
	// (Finding 5). It flips true as soon as listener.Addr() confirms
	// a non-nil bind and stays true until the daemon exits; accepting
	// flips true only after the first successful Accept.
	listenerBound atomic.Bool

	// drain is the runDaemon-provided callback that cancels the
	// Serve-ctx with a labelled cause so POST /drain can wind down
	// the accept loop without racing Exporter.Shutdown. Stored in an
	// atomic.Pointer so tests that construct statusExporter directly
	// can leave it unset without a nil dereference.
	drain atomic.Pointer[func()]

	// Kernel-module probe cache (Finding 5). kmMu serialises cache
	// updates; kmValue/kmExpiry are accessed under the mutex. The
	// cached map is returned by reference because ModuleState is a
	// value type — operators reading the status JSON cannot mutate
	// through the handle.
	kmMu     sync.Mutex
	kmValue  map[string]usbip.ModuleState
	kmExpiry time.Time
}

// newStatusExporter wires an Exporter + listener into a statusSource.
// activation is the systemd-activation flag rendered under
// listening.activation in the §7.7 status JSON; accepting starts false
// and flips true on the first successful Accept of the real listener.
// listenerBound flips true immediately when lis has a non-nil Addr so
// /readyz can distinguish "bind succeeded" from "accept loop actually
// running".
func newStatusExporter(exp *usbip.Exporter, lis net.Listener, activation bool) *statusExporter {
	s := &statusExporter{
		exp:        exp,
		listenAddr: listenerAddr(lis),
		activation: activation,
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

// BoundDevices reports the current export list. The stable one-shot
// ListAvailable snapshot is what status consumers want; streaming
// changes is a Phase 9 addition. A ListAvailable failure propagates to
// the handler so GET / can render a bound_devices_error field rather
// than masquerading the failure as an empty bound_devices array (RANK
// 12).
func (s *statusExporter) BoundDevices(ctx context.Context) ([]usbip.Device, error) {
	devs, err := s.exp.ListAvailable(ctx)
	if err != nil {
		return nil, fmt.Errorf("list bound devices: %w", err)
	}

	return devs, nil
}

// Sessions mirrors Exporter.Sessions; the caller owns the returned
// slice per the pkg/usbip contract.
func (s *statusExporter) Sessions(ctx context.Context) []usbip.Session {
	return s.exp.Sessions(ctx)
}

// Listening exposes the current accept-path state.
func (s *statusExporter) Listening() listeningState {
	return listeningState{
		Addr:       s.listenAddr,
		Activation: s.activation,
		Accepting:  s.accepting.Load(),
	}
}

// KernelModules reports the §11.5.4 triple via usbip.ProbeKernelModules
// with a kernelModuleProbeTTL cache in front of the probe (Finding 5).
// A cache hit avoids a sysfs round-trip and the slog warn that EACCES
// would otherwise log on every poll. Failures from the underlying
// probe bypass the cache so the next call retries.
func (s *statusExporter) KernelModules(ctx context.Context) (map[string]usbip.ModuleState, error) {
	s.kmMu.Lock()
	defer s.kmMu.Unlock()

	probeFn, clockFn := currentKernelModuleProbe()

	now := clockFn()
	if s.kmValue != nil && now.Before(s.kmExpiry) {
		return copyKernelModuleMap(s.kmValue), nil
	}

	mods, err := probeFn(ctx)
	if err != nil {
		return mods, err
	}

	s.kmValue = mods
	s.kmExpiry = now.Add(kernelModuleProbeTTL)

	return copyKernelModuleMap(mods), nil
}

// copyKernelModuleMap returns a shallow copy of the cached map so
// callers mutating the returned value don't corrupt the cache.
func copyKernelModuleMap(src map[string]usbip.ModuleState) map[string]usbip.ModuleState {
	out := make(map[string]usbip.ModuleState, len(src))
	maps.Copy(out, src)

	return out
}

// Drain flips accepting=false, asks the Exporter to shut down, and
// fires the run-side cancellation (if installed) so Serve returns.
// handleStatusDrain already answered 200 by the time this runs —
// errors here are observability signals only.
func (s *statusExporter) Drain(ctx context.Context) error {
	s.markAccepting(false)

	cancel := s.drain.Load()
	if cancel != nil {
		(*cancel)()
	}

	err := s.exp.Shutdown(ctx)
	if err != nil {
		return fmt.Errorf("exporter shutdown: %w", err)
	}

	return nil
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
