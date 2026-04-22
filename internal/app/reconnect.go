package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/abilisoft/usbip-go/pkg/domain"
)

// defaultStatusPollInterval is the §5.5 backstop poll period applied
// when AttachOptions.StatusPollInterval is zero. A negative value
// disables the poll entirely.
const defaultStatusPollInterval = 5 * time.Second

// defaultReconnectBackoffFloor is the first-attempt delay for the
// default exponential backoff per spec §5.5.
const defaultReconnectBackoffFloor = 1 * time.Second

// defaultReconnectBackoffCeiling is the delay ceiling for the default
// exponential backoff per spec §5.5.
const defaultReconnectBackoffCeiling = 60 * time.Second

// defaultReconnectBackoffJitter matches the spec §5.5 default
// multiplicative jitter fraction.
const defaultReconnectBackoffJitter = 0.2

// reconnectSourceUevent tags watcher log lines whose detection came
// from the KernelEvents subscription.
const reconnectSourceUevent = "uevent"

// reconnectSourcePoll tags watcher log lines whose detection came from
// the ListPorts backstop sweep.
const reconnectSourcePoll = "poll"

// reconnectParams bundles everything spawnReconnectWatcher needs for a
// single watcher lifecycle. Extracting the struct keeps the watcher
// entry-point argument list within the project's argument-limit lint
// and lets the reconnect body pass a single value through the loop.
type reconnectParams struct {
	handle   *portHandle
	portID   domain.PortID
	endpoint domain.RemoteEndpoint
	busID    domain.BusID
	opts     AttachOptions
}

// spawnReconnectWatcher enrols a new reconnect goroutine in the
// Importer waitgroup and starts it. The handle's watcherDone channel is
// created here (not at registerHandle time) so the goroutine itself is
// the sole closer. Close/Detach observe this channel to synchronise
// with the watcher's exit.
//
// The Attach caller's ctx is detached via context.WithoutCancel: the
// watcher must outlive the Attach call (spec §5.5) and its only
// termination signals are handle.done (cancelled by Detach/Close) and
// the events-source channel closing. Passing the caller ctx in and
// detaching here keeps the call graph honest for contextcheck while
// preserving the desired lifetime.
func (i *Importer) spawnReconnectWatcher(
	ctx context.Context,
	h *portHandle,
	portID domain.PortID,
	endpoint domain.RemoteEndpoint,
	busID domain.BusID,
	opts AttachOptions,
) {
	h.watcherDone = make(chan struct{})

	params := reconnectParams{
		handle:   h,
		portID:   portID,
		endpoint: endpoint,
		busID:    busID,
		opts:     resolveReconnectOptions(opts),
	}

	i.wg.Add(1)

	detached := context.WithoutCancel(ctx)

	go i.runReconnectWatcher(detached, params)
}

// resolveReconnectOptions populates the zero-valued AttachOptions fields
// with the §5.5 defaults. Mutates a copy so the caller's opts stay
// unchanged — opts is passed by value through the recursive Attach.
func resolveReconnectOptions(opts AttachOptions) AttachOptions {
	if opts.Backoff == nil {
		opts.Backoff = NewExponentialBackoff(ExponentialBackoffConfig{
			Min:    defaultReconnectBackoffFloor,
			Max:    defaultReconnectBackoffCeiling,
			Jitter: defaultReconnectBackoffJitter,
		})
	}

	if opts.StatusPollInterval == 0 {
		opts.StatusPollInterval = defaultStatusPollInterval
	}

	return opts
}

// runReconnectWatcher is the watcher goroutine body. It runs in two
// phases per spec §5.5: (1) wait for a detach signal from either the
// uevent subscription or the ListPorts backstop, (2) loop reconnect
// attempts gated by the configured backoff until success, MaxAttempts
// exhaustion, or cancellation. The watcher exits by closing
// handle.watcherDone so Detach and Close can block on it. parent is a
// detached ctx (no cancellation from the Attach caller) so the watcher
// only terminates on handle.done or Close.
func (i *Importer) runReconnectWatcher(parent context.Context, p reconnectParams) {
	defer i.wg.Done()
	defer close(p.handle.watcherDone)

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Derive the ctx cancellation from handle.done so Subscribe and
	// any ctx-aware downstream respects user Detach/Close.
	go func() {
		select {
		case <-p.handle.done:
			cancel()
		case <-ctx.Done():
		}
	}()

	events, unsubscribe, err := i.events.Subscribe(ctx)
	if err != nil {
		i.logger.Warn("reconnect watcher subscribe failed",
			slog.Any("port_id", p.portID),
			slog.Uint64("generation", p.handle.generation),
			slog.Any("err", err),
		)

		return
	}

	defer unsubscribe()

	detected := i.waitForDetach(ctx, p, events)
	if detected == "" {
		return
	}

	i.runReconnectLoop(ctx, p, detected)
}

// detectTick is the outcome of a single waitForDetach select iteration.
// Source is the detection-source tag when Done is true; when Done is
// false, the caller re-arms the poll cycle with NextPoll.
type detectTick struct {
	Source   string
	NextPoll <-chan time.Time
	Done     bool
}

// waitForDetach blocks until either a uevent for our port id arrives,
// the ListPorts backstop reports our port in StatusNull, or the watcher
// is cancelled. The returned string is the detection source name
// (reconnectSourceUevent / reconnectSourcePoll) or "" on cancellation.
func (i *Importer) waitForDetach(
	ctx context.Context,
	p reconnectParams,
	events <-chan domain.Event,
) string {
	var pollCh <-chan time.Time

	if p.opts.StatusPollInterval > 0 {
		pollCh = i.clock.After(p.opts.StatusPollInterval)
	}

	for {
		tick := i.waitForDetachTick(ctx, p, events, pollCh)
		if tick.Done {
			return tick.Source
		}

		pollCh = tick.NextPoll
	}
}

// waitForDetachTick performs a single select over the watcher's
// detection sources. Done=true indicates the outer loop should return
// Source; Done=false indicates the poll cycle must be re-armed with
// NextPoll and the loop should continue.
func (i *Importer) waitForDetachTick(
	ctx context.Context,
	p reconnectParams,
	events <-chan domain.Event,
	pollCh <-chan time.Time,
) detectTick {
	select {
	case <-ctx.Done():
		return detectTick{Done: true}
	case ev, ok := <-events:
		if !ok {
			return detectTick{Done: true}
		}

		if i.isDetachSignal(ctx, ev, p) {
			return detectTick{Source: reconnectSourceUevent, Done: true}
		}

		return detectTick{NextPoll: pollCh}
	case <-pollCh:
		if i.portIsDetached(ctx, p.portID) && i.isCurrentHandle(p.portID, p.handle) {
			return detectTick{Source: reconnectSourcePoll, Done: true}
		}

		return detectTick{NextPoll: i.clock.After(p.opts.StatusPollInterval)}
	}
}

// isDetachSignal returns true iff ev is a legitimate detach signal for
// the watcher's port: a PortDetachedEvent whose id matches p.portID,
// whose handle slot still belongs to p.handle (generation check per
// spec §5.5), AND whose detached status is confirmed by the kernel
// (defence against stale uevents that arrive after a same-slot reuse).
// The kernel confirmation step re-runs ListPorts because uevents can
// be reordered or duplicated relative to the actual sysfs state; if the
// kernel reports the port is still Used, the event is obsolete.
//
// When a PortDetachedEvent for our portID is rejected because the
// handle slot has been superseded OR because the kernel view still
// shows the port Used, the drop is logged at Debug with both the
// watcher's own generation AND the generation that now owns the slot
// (0 when no handle remains). Operators debugging a stale-event storm
// can correlate by grepping for the staleEventLogMessage.
func (i *Importer) isDetachSignal(ctx context.Context, ev domain.Event, p reconnectParams) bool {
	d, ok := ev.(domain.PortDetachedEvent)
	if !ok {
		return false
	}

	if d.Port.ID != p.portID {
		return false
	}

	if !i.isCurrentHandle(p.portID, p.handle) {
		i.logStaleEventDrop(d, p, i.currentGeneration(p.portID))

		return false
	}

	if !i.portIsDetached(ctx, p.portID) {
		// Kernel-confirmation says the port is still Used, so this
		// uevent is stale. The watcher keeps ownership (generation
		// match) but the event is ignored — log with the watcher's
		// generation in both slots so operators can distinguish the
		// "slot superseded" case above from the "kernel view
		// contradicts event" case here via the two generation
		// fields (they will be equal here, distinct above).
		i.logStaleEventDrop(d, p, p.handle.generation)

		return false
	}

	return true
}

// staleEventLogMessage is the msg field emitted on every stale-event
// drop path. Exposed as a constant so operators can grep and tests
// can lock the wording in (see reconnect_generation_test.go).
const staleEventLogMessage = "stale event ignored"

// logStaleEventDrop emits the §5.5 stale-event debug line with both
// generations and the event's port id. Current is the generation that
// currently owns the port (0 if no live handle); watcher is the
// generation held by the receiving watcher. Both names are stable
// across log schema revisions and are relied on by the 10.4b lock-in.
func (i *Importer) logStaleEventDrop(
	d domain.PortDetachedEvent, p reconnectParams, currentGen uint64,
) {
	i.logger.Debug(staleEventLogMessage,
		slog.Any("port_id", d.Port.ID),
		slog.Uint64("watcher_generation", p.handle.generation),
		slog.Uint64("current_generation", currentGen),
	)
}

// currentGeneration returns the generation of whatever handle now
// owns portID, or 0 when no handle is registered. Used by the stale-
// event log path so operators can compare "my generation" (the
// watcher's) vs "slot's current generation" (this return).
func (i *Importer) currentGeneration(id domain.PortID) uint64 {
	i.mu.RLock()
	defer i.mu.RUnlock()

	h, ok := i.handles[id]
	if !ok {
		return 0
	}

	return h.generation
}

// isCurrentHandle returns true when the Importer's handle map still
// records h as the owner of id. A false return means either (a) the
// slot was reassigned to a newer generation by a successful reattach,
// or (b) the handle was torn down entirely. In both cases, any detach
// signal targeting id is stale from this watcher's perspective — the
// newer-generation watcher (or no watcher at all) owns that slot now.
func (i *Importer) isCurrentHandle(id domain.PortID, h *portHandle) bool {
	i.mu.RLock()
	defer i.mu.RUnlock()

	cur, ok := i.handles[id]

	return ok && cur == h
}

// portIsDetached returns true when ListPorts cannot find our port id
// OR finds it in StatusNull — either outcome is the backstop signal for
// a detach per spec §5.5 item 2. ListPorts errors are swallowed at
// debug-level: the uevent path still covers the common case and a
// noisy failing sysfs probe would drown out legitimate signal.
func (i *Importer) portIsDetached(ctx context.Context, id domain.PortID) bool {
	ports, err := i.kernel.ListPorts(ctx)
	if err != nil {
		i.logger.Debug("reconnect watcher poll failed",
			slog.Any("port_id", id),
			slog.Any("err", err),
		)

		return false
	}

	for _, p := range ports {
		if p.ID == id {
			return p.Status == domain.StatusNull
		}
	}

	return true
}

// runReconnectLoop runs attempts 1..MaxAttempts (0 = infinite) gated by
// the Backoff. Each iteration sleeps, then re-attaches via the public
// Attach path so the whole dial-handshake-handoff sequence (spec §5.2)
// is exercised. On success, the old handle is removed and the loop
// exits; the replacement watcher is already running inside the
// successful Attach return.
//
// A successful Attach that follows a user-initiated Detach on the
// original handle must be rolled back: Detach sets handle.detaching
// before cancel; the watcher checks the flag after Attach returns and
// issues kernel.DetachPort on the replacement port so the device the
// user asked to release does not silently reappear (RANK 3).
func (i *Importer) runReconnectLoop(ctx context.Context, p reconnectParams, source string) {
	lastErr := fmt.Errorf("%w: port %d via %s", ErrPortDetached, p.portID, source)

	// attempt is declared outside the for-init so the give-up log line
	// below can surface the final attempt count (Finding 9); otherwise
	// the loop variable goes out of scope on exit.
	attempt := 1

	for ; p.opts.MaxAttempts == 0 || attempt <= p.opts.MaxAttempts; attempt++ {
		// Register the backoff deadline on the watcher goroutine
		// BEFORE firing OnReconnect. Tests that synchronise on the
		// callback (TestImporterReconnectBackoffRespected being the
		// canonical one) then call clk.Advance(delay) and expect the
		// deadline to fire; pre-fix OnReconnect fired first, so the
		// FakeClock's pending list was empty at Advance time and the
		// timer registered later against the already-advanced now —
		// the watcher then waited for another full delay that no
		// Advance ever delivered. Register-first makes the callback a
		// sound sync point for deterministic clock control.
		delayCh := i.armBackoff(p, attempt)

		i.fireOnReconnect(p.opts.OnReconnect, attempt, lastErr, p.portID, source)

		if !i.waitBackoffChan(ctx, delayCh) {
			i.metrics.ImporterReconnectAttempt(ReconnectOutcomeCanceled)

			return
		}

		newPort, err := i.Attach(ctx, p.endpoint, p.busID, p.opts)
		if err == nil {
			i.finishReconnectSuccess(ctx, newPort, p, attempt, source)

			return
		}

		lastErr = err

		if errors.Is(err, ErrImporterClosed) {
			i.metrics.ImporterReconnectAttempt(ReconnectOutcomeCanceled)

			return
		}

		i.metrics.ImporterReconnectAttempt(ReconnectOutcomeBackoff)

		i.logger.Warn("reconnect attempt failed",
			slog.Any("port_id", p.portID),
			slog.Int("attempt", attempt),
			slog.String("source", source),
			slog.Any("err", err),
		)
	}

	i.metrics.ImporterReconnectAttempt(ReconnectOutcomeExhausted)
	i.removeHandle(p.portID, p.handle)
	// Refresh usbip_importer_ports_active to reflect the retired
	// handle slot (Pass-4 RANK 3). Pre-fix the gauge stayed inflated
	// from the original Attach until the next gauge-updating event,
	// misleading operators about live-port count.
	i.updateImporterPortsGauge()
	// attempt is the for-loop's post-exit value: when the condition
	// fails (attempt > MaxAttempts), the final attempted-and-failed
	// iteration is attempt-1 (Finding 9). MaxAttempts==0 (infinite) is
	// unreachable here because the loop only exits on success return.
	i.logger.Warn("reconnect giving up after max attempts",
		slog.Any("port_id", p.portID),
		slog.Int("attempt", attempt-1),
		slog.String("source", source),
		slog.Int("max_attempts", p.opts.MaxAttempts),
		slog.Any("last_err", lastErr),
	)
}

// finishReconnectSuccess handles the post-Attach success branch of the
// reconnect loop. If the original handle is flagged detaching (RANK 3)
// the replacement kernel port is rolled back; otherwise the original
// port id is removed and the success is logged. Extracted to keep
// runReconnectLoop within the project's cognitive-complexity cap.
func (i *Importer) finishReconnectSuccess(
	ctx context.Context,
	newPort domain.Port,
	p reconnectParams,
	attempt int,
	source string,
) {
	if p.handle.detaching.Load() {
		// Detach bounded-waited past our wedged Attach and removed
		// the original handle already; the user expects the device
		// to stay gone. Roll back the replacement kernel handoff
		// before it wins the race (RANK 3).
		i.rollbackSupersededReconnect(ctx, newPort.ID, p, source)
		i.metrics.ImporterReconnectAttempt(ReconnectOutcomeCanceled)

		return
	}

	i.metrics.ImporterReconnectAttempt(ReconnectOutcomeOK)
	i.removeHandle(p.portID, p.handle)
	// Refresh usbip_importer_ports_active so the old slot's retirement
	// nets out the Attach-time gauge bump the replacement already
	// performed (Pass-4 RANK 3). When the kernel lands the replacement
	// on a different PortID than the original, the gauge would
	// otherwise stay inflated by one — the rollback path already does
	// this; the success path was missing the symmetric refresh.
	i.updateImporterPortsGauge()

	// Per spec §5.5 / BackoffStrategy contract (internal/app/backoff.go:20
	// and pkg/usbip/backoff.go:19): "Reset is called after a successful
	// reconnect so the next failure starts from the smallest delay
	// again." Pre pass-3 RANK 4 this call was missing — a stateful
	// backoff stayed escalated across outages and the next failure
	// paid the last-attempt delay instead of the configured floor.
	// Reset is NOT invoked on the rollback-superseded branch above:
	// that path is not a user-visible success (the kernel port is
	// about to be detached). The Backoff field is typed as an
	// interface with nil-zero; guard before the call so a caller that
	// passes nil does not crash.
	if p.opts.Backoff != nil {
		p.opts.Backoff.Reset()
	}

	i.logger.Info("reconnect succeeded",
		slog.Any("old_port_id", p.portID),
		slog.Any("new_port_id", newPort.ID),
		slog.Int("attempt", attempt),
		slog.String("source", source),
	)
}

// rollbackSupersededReconnect releases the kernel port that a
// reconnect-path Attach acquired after the user's Detach had already
// bounded-waited past the wedged watcher (RANK 3). The replacement
// handle is already registered in the map by Attach's finishAttach
// call.
//
// Pass-2 RANK 2: kernel.DetachPort fires BEFORE the handle map entry
// is removed, and the entry is removed only on DetachPort success. If
// DetachPort fails the handle stays registered so the user can retry
// Detach(newID) and eventually release the kernel port. Pre-fix the
// entry was deleted unconditionally, stranding the kernel port with
// no owner on the failure path.
//
// The fresh handle's reconnect watcher (if any) is cancelled either
// way — the user has expressed intent for the device to stay gone;
// letting the watcher keep running would spawn another Attempt on
// every detach uevent.
func (i *Importer) rollbackSupersededReconnect(
	ctx context.Context, newID domain.PortID, p reconnectParams, source string,
) {
	i.mu.RLock()

	fresh, ok := i.handles[newID]

	i.mu.RUnlock()

	if ok && fresh != nil {
		fresh.cancel()
	}

	err := i.kernel.DetachPort(ctx, newID)
	if err != nil {
		i.logger.Warn("rollback reconnect detach failed; handle preserved for retry",
			slog.Any("new_port_id", newID),
			slog.Any("old_port_id", p.portID),
			slog.String("source", source),
			slog.Any("err", err),
		)

		// Handle intentionally left in the map so a user retry of
		// Detach(newID) can drive a fresh kernel.DetachPort call.
		return
	}

	i.mu.Lock()

	if cur, stillOurs := i.handles[newID]; stillOurs && cur == fresh {
		delete(i.handles, newID)
	}

	i.mu.Unlock()

	i.logger.Info("reconnect rolled back after Detach",
		slog.Any("new_port_id", newID),
		slog.Any("old_port_id", p.portID),
		slog.String("source", source),
	)

	i.updateImporterPortsGauge()
}

// armBackoff computes Backoff.Next(attempt-1) and, when positive,
// registers the deadline with the injected Clock synchronously on the
// watcher goroutine. The returned channel is the After-channel the
// subsequent waitBackoffChan call parks on; nil means the backoff is
// zero (or negative) and the wait is a no-op.
//
// Registering the deadline BEFORE the runReconnectLoop iteration fires
// OnReconnect is the ordering invariant that makes the OnReconnect
// callback a sound synchronisation point for tests driving the
// FakeClock. See runReconnectLoop for the rationale.
func (i *Importer) armBackoff(p reconnectParams, attempt int) <-chan time.Time {
	delay := p.opts.Backoff.Next(attempt - 1)
	if delay <= 0 {
		return nil
	}

	return i.clock.After(delay)
}

// waitBackoffChan blocks until either ch fires (backoff elapsed) or
// ctx cancellation interrupts. A nil ch denotes a zero-delay backoff
// and the wait completes immediately with the result of a single
// ctx.Err() check so a ctx that was cancelled between the arm call and
// this wait still aborts the reconnect attempt. Returns true when the
// caller may proceed with the next Attach, false to exit the loop
// with ReconnectOutcomeCanceled.
func (i *Importer) waitBackoffChan(ctx context.Context, ch <-chan time.Time) bool {
	if ch == nil {
		return ctx.Err() == nil
	}

	select {
	case <-ctx.Done():
		return false
	case <-ch:
		// If ctx was also ready the select picks uniformly at random;
		// a second ctx check after the channel branch ensures a
		// cancellation always wins on tie, so a watcher whose handle
		// was superseded mid-backoff cannot sneak a spurious Attach
		// past the just-cancelled ctx (reconnect_test.go flake:
		// TestImporterReconnectSupersededWatcherDropsEvent).
		return ctx.Err() == nil
	}
}

// removeHandle deletes the old port id from the handle map when the
// watcher still owns it. A concurrent Detach may have already removed
// the entry; the map delete is idempotent so no guard is needed.
func (i *Importer) removeHandle(id domain.PortID, owned *portHandle) {
	i.mu.Lock()
	defer i.mu.Unlock()

	// Only remove if the slot still points to our handle. A slot that
	// was re-used by a subsequent Attach would carry a different
	// pointer; deleting it would unbind a live port.
	if cur, ok := i.handles[id]; ok && cur == owned {
		delete(i.handles, id)
	}
}

// fireOnReconnect invokes cb in a fresh goroutine with panic recovery.
// Running off the watcher goroutine isolates a slow callback from the
// retry cadence (the watcher must stay responsive to ctx cancellation);
// the recover block logs and drops panics so a buggy caller cannot
// crash the process or leave the watcher in an indeterminate state.
// cb may run concurrently with other Importer operations — callers who
// need synchronous semantics must wire their own buffering.
//
// portID and source are logged on the panic-recovery path (Finding 9)
// so operators can correlate the panic with the affected device and
// the detach-detection source (uevent vs poll) that drove the retry
// loop.
//
// The callback goroutine is enrolled in i.wg so Close's bounded drain
// observes it (RANK 11). Pre-fix, the raw `go` spawn escaped the wg
// and a blocking callback could outlive Close indefinitely; post-fix,
// Close.waitGroupBounded waits up to shutdownTimeout on the tracked
// goroutine before returning.
func (i *Importer) fireOnReconnect(
	cb func(int, error),
	attempt int,
	err error,
	portID domain.PortID,
	source string,
) {
	if cb == nil {
		return
	}

	i.wg.Go(func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}

			i.logger.Error("OnReconnect callback panicked",
				slog.Uint64("port_id", uint64(portID)),
				slog.Int("attempt", attempt),
				slog.String("source", source),
				slog.Any("panic", r),
			)
		}()

		cb(attempt, err)
	})
}
