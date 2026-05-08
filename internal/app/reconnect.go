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
func (i *Importer) isDetachSignal(ctx context.Context, ev domain.Event, p reconnectParams) bool {
	d, ok := ev.(domain.PortDetachedEvent)
	if !ok {
		return false
	}

	if d.Port.ID != p.portID {
		return false
	}

	if !i.isCurrentHandle(p.portID, p.handle) {
		return false
	}

	return i.portIsDetached(ctx, p.portID)
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
func (i *Importer) runReconnectLoop(ctx context.Context, p reconnectParams, source string) {
	lastErr := fmt.Errorf("%w: port %d via %s", ErrPortDetached, p.portID, source)

	// attempt is declared outside the for-init so the give-up log line
	// below can surface the final attempt count (Finding 9); otherwise
	// the loop variable goes out of scope on exit.
	attempt := 1

	for ; p.opts.MaxAttempts == 0 || attempt <= p.opts.MaxAttempts; attempt++ {
		i.fireOnReconnect(p.opts.OnReconnect, attempt, lastErr, p.portID, source)

		if !i.waitBackoff(ctx, p, attempt) {
			i.metrics.ImporterReconnectAttempt(ReconnectOutcomeCanceled)

			return
		}

		newPort, err := i.Attach(ctx, p.endpoint, p.busID, p.opts)
		if err == nil {
			i.metrics.ImporterReconnectAttempt(ReconnectOutcomeOK)
			i.removeHandle(p.portID, p.handle)
			i.logger.Info("reconnect succeeded",
				slog.Any("old_port_id", p.portID),
				slog.Any("new_port_id", newPort.ID),
				slog.Int("attempt", attempt),
				slog.String("source", source),
			)

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

// waitBackoff sleeps for Backoff.Next(attempt-1) using the injected
// Clock. Returns false when cancellation fires during the sleep so the
// caller can exit without issuing a reconnect attempt.
func (i *Importer) waitBackoff(ctx context.Context, p reconnectParams, attempt int) bool {
	delay := p.opts.Backoff.Next(attempt - 1)
	if delay <= 0 {
		return ctx.Err() == nil
	}

	timer := i.clock.After(delay)

	select {
	case <-ctx.Done():
		return false
	case <-timer:
		return true
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

	go func() {
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
	}()
}
