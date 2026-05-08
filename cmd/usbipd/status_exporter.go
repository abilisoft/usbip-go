package main

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	"github.com/abilisoft/usbip-go/pkg/usbip"
)

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

	// drain is the runDaemon-provided callback that cancels the
	// Serve-ctx with a labelled cause so POST /drain can wind down
	// the accept loop without racing Exporter.Shutdown. Stored in an
	// atomic.Pointer so tests that construct statusExporter directly
	// can leave it unset without a nil dereference.
	drain atomic.Pointer[func()]
}

// newStatusExporter wires an Exporter + listener into a statusSource.
// activation is the systemd-activation flag rendered under
// listening.activation in the §7.7 status JSON; accepting starts false
// and flips true once runDaemon calls Serve.
func newStatusExporter(exp *usbip.Exporter, lis net.Listener, activation bool) *statusExporter {
	return &statusExporter{
		exp:        exp,
		listenAddr: listenerAddr(lis),
		activation: activation,
	}
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
// changes is a Phase 9 addition.
func (s *statusExporter) BoundDevices(ctx context.Context) []usbip.Device {
	devs, err := s.exp.ListAvailable(ctx)
	if err != nil {
		return []usbip.Device{}
	}

	return devs
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

// KernelModules reports the §11.5.4 triple via pkg/usbip.ProbeKernelModules.
// Errors are wrapped with the callsite tag so wrapcheck is satisfied
// and operators reading logs can distinguish probe failures from other
// status-handler diagnostics.
func (s *statusExporter) KernelModules(ctx context.Context) (map[string]string, error) {
	mods, err := usbip.ProbeKernelModules(ctx)
	if err != nil {
		return mods, fmt.Errorf("probe kernel modules: %w", err)
	}

	return mods, nil
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
